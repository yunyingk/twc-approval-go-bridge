package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
	baseevents "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/invoiceledger"
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
	if len(os.Args) > 1 {
		isDoctor := os.Args[1] == "doctor"
		isBusinessCheck := os.Args[1] == "check-business-config"

		if isDoctor || isBusinessCheck {
			if len(os.Args) > 3 {
				logger.Error("usage: server doctor [-json] | server check-business-config")
				os.Exit(2)
			}
		} else if len(os.Args) != 3 {
			logger.Error("usage: server doctor [-json] | server check-business-config | {notify-transaction|review-status|check-review|preview-review|submit-review|retry-writeback|apply-seal-result} <record-id-document-id-or-file>")
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		switch os.Args[1] {
		case "doctor":
			jsonOutput := len(os.Args) > 2 && os.Args[2] == "-json"
			err = runDoctor(ctx, cfg, jsonOutput, os.Stdout)
		case "check-business-config":
			err = runBusinessCheck(ctx, cfg, os.Stdout)
		case "notify-transaction":
			err = runNotifyTransaction(ctx, cfg, os.Args[2], os.Stdout)
		case "preview-review":
			err = runReviewPreview(ctx, cfg, os.Args[2], os.Stdout)
		case "review-status":
			err = runReviewInspection(ctx, cfg, os.Args[2], false, os.Stdout)
		case "check-review":
			err = runReviewInspection(ctx, cfg, os.Args[2], true, os.Stdout)
		case "retry-writeback":
			err = runReviewWriteback(ctx, cfg, os.Args[2], os.Stdout)
		case "submit-review":
			err = runReviewSubmit(ctx, cfg, os.Args[2], logger)
		case "apply-seal-result":
			err = runSealResult(ctx, cfg, os.Args[2])
		default:
			err = errors.New("unknown command")
		}
		if err != nil {
			logger.Error("command failed", "error", err)
			os.Exit(1)
		}
		return
	}
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
			os.Exit(1)
		}
	case err := <-feishuErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Feishu long connection stopped unexpectedly", "error", err)
			shutdown()
			os.Exit(1)
		}
		shutdown()
	case <-ctx.Done():
		shutdown()
	}
	logger.Info("service stopped")
}
