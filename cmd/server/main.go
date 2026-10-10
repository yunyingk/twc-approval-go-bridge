package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/anyreceipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
	baseevents "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/invoiceledger"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/card"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/task"
	"github.com/yunyingk/twc-approval-go-bridge/internal/httpserver"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
	"github.com/yunyingk/twc-approval-go-bridge/internal/version"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Runtime.LogLevel}))

	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	cmd := os.Args[1]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		printUsage(os.Stdout)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var cmdErr error
	switch cmd {
	case "server", "start", "run":
		cmdErr = runServer(ctx, cfg, logger, stop)
	case "doctor":
		jsonOutput := len(os.Args) > 2 && os.Args[2] == "-json"
		cmdErr = runDoctor(ctx, cfg, jsonOutput, os.Stdout)
	case "check-business-config":
		cmdErr = runBusinessCheck(ctx, cfg, os.Stdout)
	case "init-bitable":
		cmdErr = runInitBitable(ctx, cfg, os.Args[2:], os.Stdout)
	case "add-admin":
		if len(os.Args) < 4 {
			cmdErr = errors.New("usage: add-admin <base-token> <user-email-or-open-id>")
		} else {
			cmdErr = runAddAdmin(ctx, cfg, os.Args[2], os.Args[3], os.Stdout)
		}
	case "transfer-owner":
		if len(os.Args) < 4 {
			cmdErr = errors.New("usage: transfer-owner <base-token> <user-open-id>")
		} else {
			cmdErr = runTransferOwner(ctx, cfg, os.Args[2], os.Args[3], os.Stdout)
		}
	case "notify-transaction":
		if len(os.Args) < 3 {
			cmdErr = errors.New("usage: notify-transaction <record-id>")
		} else {
			cmdErr = runNotifyTransaction(ctx, cfg, os.Args[2], os.Stdout)
		}
	case "scan-transactions":
		cmdErr = runScanTransactions(ctx, cfg, logger, os.Args[2:], os.Stdout)
	case "preview-review":
		if len(os.Args) < 3 {
			cmdErr = errors.New("usage: preview-review <record-id>")
		} else {
			cmdErr = runReviewPreview(ctx, cfg, os.Args[2], os.Stdout)
		}
	case "review-status":
		if len(os.Args) < 3 {
			cmdErr = errors.New("usage: review-status <record-id>")
		} else {
			cmdErr = runReviewInspection(ctx, cfg, os.Args[2], false, os.Stdout)
		}
	case "check-review":
		if len(os.Args) < 3 {
			cmdErr = errors.New("usage: check-review <record-id>")
		} else {
			cmdErr = runReviewInspection(ctx, cfg, os.Args[2], true, os.Stdout)
		}
	case "retry-writeback":
		if len(os.Args) < 3 {
			cmdErr = errors.New("usage: retry-writeback <record-id>")
		} else {
			cmdErr = runReviewWriteback(ctx, cfg, os.Args[2], os.Stdout)
		}
	case "submit-review":
		if len(os.Args) < 3 {
			cmdErr = errors.New("usage: submit-review <record-id>")
		} else {
			cmdErr = runReviewSubmit(ctx, cfg, os.Args[2], logger)
		}
	case "apply-seal-result":
		if len(os.Args) < 3 {
			cmdErr = errors.New("usage: apply-seal-result <file>")
		} else {
			cmdErr = runSealResult(ctx, cfg, os.Args[2])
		}
	case "export-skill":
		cmdErr = runExportSkill(os.Args[2:], os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n", cmd)
		printUsage(os.Stderr)
		os.Exit(2)
	}
	if cmdErr != nil {
		logger.Error("command failed", "error", cmdErr)
		os.Exit(1)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `海外小票识别与 AI 审核桥接服务 (twc-approval-go-bridge)

用法:
  twc-approval-go-bridge <command> [arguments]

核心服务:
  server                   启动常驻后台服务（长连接监听、小票识别、AI 审核与回调服务）

运维与诊断:
  doctor [-json]           全面健康检查与权限探测
  init-bitable [options]   一键在飞书创建多维表格储备库并自动配置
  check-business-config    校验当前多维表格拓扑与字段契约
  add-admin <base> <user>  为多维表格添加管理员协作者 (full_access)
  transfer-owner <base> <user> 转移多维表格所有权

单据与审核工具:
  notify-transaction <id>  向持卡人投递补票通知卡片
  scan-transactions        全量扫描流水表并按独立规则批量自动催报
  preview-review <id>      预览单据 AI 审核请求载荷
  submit-review <id>       手动送审单据并触发机审
  review-status <id>       查看单据机审状态与留痕
  check-review <id>        校验单据机审结果与一致性
  retry-writeback <id>     手动重试机审结果回写
  apply-seal-result <file> 本地测试应用 SealAI 回调结果
  export-skill [-o path]   导出内嵌的豆包企业内置技能配置文档 (.md)
`)
}

func runServer(ctx context.Context, cfg config.Config, logger *slog.Logger, stop context.CancelFunc) error {
	var err error
	if cfg.Business != nil {
		details := cfg.Business.Tables.ReimbursementDetails
		ledger := cfg.Business.Tables.InvoiceLedger
		logger.Info("business profile selected", "profile", cfg.Business.Name, "config_file", cfg.ConfigFile,
			"source_base", details.BaseToken, "source_table", details.TableID, "ledger_table", ledger.TableID,
			"receipt_provider", cfg.ReceiptProvider(), "review_provider", cfg.ReviewProvider(), "review_trigger", cfg.ReviewTriggerMode(),
			"include_transactions", cfg.Business.Review.IncludeTransactions)
		logger.Info("review change settings", "resubmit_on_detail_change", cfg.Business.Review.ResubmitOnDetailChange,
			"resubmit_on_source_change", cfg.Business.Review.ResubmitOnSourceChange,
			"change_debounce", cfg.Business.Review.ChangeDebounce)
	}
	server := httpserver.New(cfg.Runtime.HTTPAddr, logger, version.Version)
	if cfg.Runtime.DashboardPassword != "" {
		server.SetPassword(cfg.Runtime.DashboardPassword)
	}
	server.Register("POST /seal/callback/mock", seal.MockCallback(logger))

	var reviewService *appreview.Service
	if cfg.Seal.CallbackToken != "" {
		reviewService, err = newReviewDeliveryService(cfg)
		if err != nil {
			logger.Error("configure Seal callback", "error", err)
			os.Exit(1)
		}
		server.Register("POST /seal/callback/{token}", seal.CallbackHandler(cfg.Seal.CallbackToken, reviewService))
	}
	if reviewService == nil && cfg.Business != nil && len(cfg.Business.Review.ResultFields) > 0 {
		reviewService, err = newReviewDeliveryService(cfg)
		if err != nil {
			logger.Error("configure review writeback", "error", err)
			os.Exit(1)
		}
	}
	var automaticReview *appreview.Automatic
	var sourceChanges *appreview.SourceChanges
	if cfg.ReviewTriggerMode() == "after_recognition" {
		// Automatic submissions follow review provider independently of a Seal
		// callback receiver that may still finish older in-flight Seal requests.
		submitter, err := newReviewService(cfg)
		if err != nil {
			logger.Error("configure automatic review provider", "error", err)
			os.Exit(1)
		}
		triggers, storeErr := state.NewFiles(cfg.Runtime.StateDir)
		if storeErr != nil {
			logger.Error("configure automatic review state", "error", storeErr)
			os.Exit(1)
		}
		details := cfg.Business.Tables.ReimbursementDetails
		automaticReview, err = appreview.NewAutomatic(submitter, triggers, "feishu:"+details.BaseToken+":"+details.TableID, logger)
		if err != nil {
			logger.Error("configure automatic review", "error", err)
			os.Exit(1)
		}
		if reviewChangesEnabled(cfg) {
			debounce := 10 * time.Second
			if cfg.Business.Review.ChangeDebounce != "" {
				debounce, _ = time.ParseDuration(cfg.Business.Review.ChangeDebounce) // validated during configuration loading
			}
			if err := automaticReview.EnableChanges(debounce); err != nil {
				logger.Error("configure review changes", "error", err)
				os.Exit(1)
			}
		}
	}
	var baseEventSink events.Sink
	var receiptFlow *recognition.Processor
	var attachmentClient *base.AttachmentClient
	if cfg.FeishuEnabled() {
		sink := events.LoggingSink(logger, cfg.Feishu.LogRawEvents)
		receiptProvider := cfg.ReceiptProvider()
		if receiptProvider != "" {
			var recognizer invoice.Recognizer
			switch receiptProvider {
			case "anyreceipt":
				recognizer, err = newAnyreceipt(cfg.Anyreceipt.APIKey)
			case "model":
				recognizer, err = newModel(cfg.Model.APIKey, cfg.Model.BaseURL, cfg.Model.Name)
			default:
				err = fmt.Errorf("unsupported receipt recognition provider %q", receiptProvider)
			}
			if err != nil {
				logger.Error("configure receipt recognizer", "error", err)
				os.Exit(1)
			}
			attachmentClient = base.NewAttachmentClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
			resultHandler := recognition.ResultHandler(func(ctx context.Context, result recognition.Result) error {
				logger.InfoContext(ctx, "receipt recognized", "trigger", result.Trigger, "record_id", result.RecordID, "file_name", result.FileName, "output_fields", len(result.Recognition.Outputs))
				return nil
			})
			details := cfg.Business.Tables.ReimbursementDetails
			ledger := cfg.Business.Tables.InvoiceLedger

			var taskService *task.Service
			if cfg.FeishuEnabled() {
				var taskStore task.Store
				if cfg.Runtime.StateDir != "" {
					if st, err := state.NewFiles(cfg.Runtime.StateDir); err == nil {
						taskStore = st
					}
				}
				taskClient := task.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
				taskService = task.NewService(taskClient, taskStore)
			}

			if ledger.TableID != "" {
				ledgerHandler, ledgerErr := invoiceledger.New(invoiceledger.Config{BaseToken: details.BaseToken, SourceTableID: details.TableID, SourceDetailFieldID: details.DetailIDField(), TableID: ledger.TableID, Fields: ledger.Fields}, base.NewLedgerClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret), logger)
				if ledgerErr != nil {
					logger.Error("configure invoice ledger", "error", ledgerErr)
					os.Exit(1)
				}
				resultHandler = func(ctx context.Context, result recognition.Result) error {
					if err := ledgerHandler.Handle(ctx, result); err != nil {
						return err
					}
					if taskService != nil {
						if err := taskService.CompleteOnInvoiceUpload(ctx, result.RecordID, result.FileName, ""); err != nil {
							logger.WarnContext(ctx, "complete task on invoice upload failed", "record_id", result.RecordID, "error", err)
						} else {
							logger.InfoContext(ctx, "task completed on invoice upload", "record_id", result.RecordID, "file_name", result.FileName)
						}
					}
					if automaticReview != nil {
						return automaticReview.NotifyRecognition(ctx, result.RecordID)
					}
					return nil
				}
			}
			receiptFlow, err = recognition.New(recognition.Config{BaseToken: details.BaseToken, TableID: details.TableID, FieldID: details.AttachmentField()}, attachmentClient, recognizer, resultHandler, logger)
			if err != nil {
				logger.Error("configure receipt flow", "error", err)
				os.Exit(1)
			}
			checkpoints, cacheErr := state.NewFiles(cfg.Runtime.StateDir)
			if cacheErr != nil {
				logger.Error("configure receipt state", "error", cacheErr)
				os.Exit(1)
			}
			scopeData, _ := json.Marshal(struct {
				Base, Table, Field, Provider, Model, Endpoint, Ledger string
				Fields                                                map[string]string
			}{details.BaseToken, details.TableID, details.AttachmentField(), receiptProvider, cfg.Model.Name, cfg.Model.BaseURL, ledger.TableID, ledger.Fields})
			lease, leaseErr := checkpoints.AcquireWorker(details.BaseToken + ":" + details.TableID + ":" + details.AttachmentField())
			if leaseErr != nil {
				logger.Error("claim receipt worker", "error", leaseErr)
				os.Exit(1)
			}
			defer lease.Close()
			scopeHash := sha256.Sum256(scopeData)
			receiptFlow.WithCheckpoints(checkpoints, hex.EncodeToString(scopeHash[:]))
			if cfg.ReceiptTriggerMode() != "poll" {
				logEvent := sink
				attachmentSink := baseevents.NewAttachmentSink(recognition.Config{BaseToken: details.BaseToken, TableID: details.TableID, FieldID: details.AttachmentField()}, receiptFlow).Sink
				sink = func(ctx context.Context, event events.Event) error {
					if err := logEvent(ctx, event); err != nil {
						return err
					}
					return attachmentSink(ctx, event)
				}
			}
		}
		if automaticReview != nil && cfg.Business != nil && cfg.Business.Review.ResubmitOnDetailChange {
			details := cfg.Business.Tables.ReimbursementDetails
			fieldIDs := []string{details.AttachmentField(), details.InvoiceRelationField()}
			for _, id := range cfg.Business.Review.ContextFields {
				fieldIDs = append(fieldIDs, id)
			}
			if cfg.Business.Review.IncludeTransactions {
				fieldIDs = append(fieldIDs, details.Fields["transaction_relation"])
			}
			changeSink, changeErr := baseevents.NewReviewChangeSink(details.BaseToken, details.TableID, fieldIDs, automaticReview)
			if changeErr != nil {
				logger.Error("configure review change events", "error", changeErr)
				os.Exit(1)
			}
			previousSink := sink
			sink = func(ctx context.Context, event events.Event) error {
				if err := previousSink(ctx, event); err != nil {
					return err
				}
				return changeSink.Sink(ctx, event)
			}
		}
		if automaticReview != nil && cfg.Business != nil && cfg.Business.Review.ResubmitOnSourceChange {
			var sourceSink *baseevents.ReviewSourceChangeSink
			sourceChanges, sourceSink, err = newReviewSourceChanges(cfg, automaticReview, logger)
			if err != nil {
				logger.Error("configure review source changes", "error", err)
				os.Exit(1)
			}
			previousSink := sink
			sink = func(ctx context.Context, event events.Event) error {
				if err := previousSink(ctx, event); err != nil {
					return err
				}
				return sourceSink.Sink(ctx, event)
			}
		}
		if cfg.ReceiptProvider() == "" || cfg.ReceiptTriggerMode() != "poll" || reviewChangesEnabled(cfg) {
			baseEventSink = sink
		}
	} else {
		logger.Info("Feishu long connection disabled", "reason", "credentials not configured")
	}
	var listeners []*events.Listener
	if baseEventSink != nil {
		eventType := strings.TrimSpace(cfg.Feishu.EventType)
		if eventType == "" {
			eventType = events.DefaultEventType
		}
		listener, err := events.New(cfg.Feishu.AppID, cfg.Feishu.AppSecret, eventType, baseEventSink, logger)
		if err != nil {
			logger.Error("create Feishu listener", "error", err)
			os.Exit(1)
		}
		listeners = append(listeners, listener)
	}

	startTime := time.Now()

	var (
		anyreceiptMu        sync.RWMutex
		lastAnyreceiptUsage *anyreceipt.Usage
		lastAnyreceiptErr   error
		lastAnyreceiptTime  time.Time
	)

	fetchAnyreceiptUsage := func() {
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		httpClient := &http.Client{Timeout: 15 * time.Second}
		cli, err := anyreceipt.New(cfg.Anyreceipt.APIKey, httpClient)
		if err != nil {
			anyreceiptMu.Lock()
			lastAnyreceiptErr = err
			lastAnyreceiptTime = time.Now()
			anyreceiptMu.Unlock()
			return
		}
		usage, err := cli.GetUsage(ctxTimeout)
		anyreceiptMu.Lock()
		if err == nil {
			lastAnyreceiptUsage = usage
			lastAnyreceiptErr = nil
			lastAnyreceiptTime = time.Now()
		} else {
			lastAnyreceiptErr = err
			// If error, allow retry after 5 seconds instead of 30 seconds
			lastAnyreceiptTime = time.Now().Add(-25 * time.Second)
		}
		anyreceiptMu.Unlock()
	}

	if strings.TrimSpace(cfg.Anyreceipt.APIKey) != "" {
		go fetchAnyreceiptUsage()
	}

	getAnyreceiptStatus := func() *httpserver.AnyreceiptStatus {
		if strings.TrimSpace(cfg.Anyreceipt.APIKey) == "" {
			return nil
		}
		anyreceiptMu.RLock()
		u := lastAnyreceiptUsage
		uErr := lastAnyreceiptErr
		uTime := lastAnyreceiptTime
		anyreceiptMu.RUnlock()

		if time.Since(uTime) > 30*time.Second {
			go fetchAnyreceiptUsage()
		}

		res := &httpserver.AnyreceiptStatus{
			Configured: true,
		}
		if u != nil {
			res.BalancePoints = u.BalancePoints
			res.AvailableCallCount = u.AvailableCallCount
			res.CallCostPoints = u.CallCostPoints
		}
		if uErr != nil {
			res.Error = uErr.Error()
		}
		return res
	}

	server.SetStatusProvider(func() httpserver.Status {
		feishuState := "disabled"
		var feishuConnected, feishuLastEvent string
		var feishuEvents int64
		if len(listeners) > 0 {
			state, connAt, lastEv, count := listeners[0].Status()
			feishuState = state
			feishuEvents = count
			if !connAt.IsZero() {
				feishuConnected = connAt.Format("2006-01-02 15:04:05")
			}
			if !lastEv.IsZero() {
				feishuLastEvent = lastEv.Format("2006-01-02 15:04:05")
			}
		}

		uptime := time.Since(startTime).Truncate(time.Second)
		var baseToken, baseURL string
		tables := make(map[string]string)
		if cfg.Business != nil {
			baseToken = cfg.Business.Tables.ReimbursementDetails.BaseToken
			if cfg.Feishu.NormalizedHost() != "" && baseToken != "" {
				baseURL = fmt.Sprintf("%s/base/%s", cfg.Feishu.NormalizedHost(), baseToken)
			}
			tables["交易流水表"] = cfg.Business.Tables.Transactions.TableID
			tables["个人报销明细"] = cfg.Business.Tables.ReimbursementDetails.TableID
			tables["发票台账"] = cfg.Business.Tables.InvoiceLedger.TableID
		}

		return httpserver.Status{
			Version:         version.Version,
			HTTPAddr:        cfg.Runtime.HTTPAddr,
			StartTime:       startTime,
			Uptime:          uptime.String(),
			UptimeSeconds:   int64(uptime.Seconds()),
			FeishuState:     feishuState,
			FeishuEvents:    feishuEvents,
			FeishuConnected: feishuConnected,
			FeishuLastEvent: feishuLastEvent,
			ReceiptProvider: cfg.ReceiptProvider(),
			ReviewProvider:  cfg.ReviewProvider(),
			ReviewTrigger:   cfg.ReviewTriggerMode(),
			BaseToken:       baseToken,
			BaseURL:         baseURL,
			Tables:          tables,
			Anyreceipt:      getAnyreceiptStatus(),
		}
	})

	if automaticReview != nil {
		go automaticReview.Run(ctx)
	}
	if sourceChanges != nil {
		go sourceChanges.Run(ctx)
	}
	if reviewService != nil {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				if err := reviewService.RetryWritebacks(ctx); err != nil && ctx.Err() == nil {
					logger.ErrorContext(ctx, "retry review writeback", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	if receiptFlow != nil {
		go receiptFlow.Run(ctx)
		if cfg.ReceiptTriggerMode() != "event" {
			go receiptFlow.Poll(ctx, attachmentClient, cfg.ReceiptPollInterval, cfg.ReceiptPollStartup == "process")
		}
	}
	if cfg.Transactions.AutoNotify && cfg.FeishuEnabled() && cfg.Business != nil && cfg.Business.Tables.Transactions.TableID != "" {
		cardSvc, cardErr := card.NewService(cfg)
		if cardErr == nil {
			scanner := card.NewScanner(cardSvc, logger)
			go scanner.Start(ctx, cfg.Transactions.PollInterval)
		} else {
			logger.Warn("transaction scanner initialization failed", "error", cardErr)
		}
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server started", "addr", cfg.Runtime.HTTPAddr, "version", version.Version)
		serverErr <- server.ListenAndServe()
	}()

	var feishuErr <-chan error
	if len(listeners) > 0 {
		listenerErr := make(chan error, len(listeners))
		feishuErr = listenerErr
		for _, listener := range listeners {
			go func() {
				logger.Info("starting Feishu long connection")
				listenerErr <- listener.Start(ctx)
			}()
		}
	}

	shutdown := func() {
		stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Runtime.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("graceful shutdown failed", "error", err)
		}
		for _, listener := range listeners {
			if err := listener.CloseAndWait(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("Feishu listener shutdown failed", "error", err)
			}
		}
	}

	select {
	case err := <-serverErr:
		if err != nil {
			logger.Error("http server stopped unexpectedly", "error", err)
			shutdown()
			return err
		}
	case err := <-feishuErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Feishu long connection stopped unexpectedly", "error", err)
			shutdown()
			return err
		}
		shutdown()
	case <-ctx.Done():
		shutdown()
	}
	logger.Info("service stopped")
	return nil
}
