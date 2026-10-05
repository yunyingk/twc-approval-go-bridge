package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/invoiceledger"
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

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if len(os.Args) > 1 {
		if (os.Args[1] == "check-business-config" && len(os.Args) != 2) || (os.Args[1] != "check-business-config" && len(os.Args) != 3) {
			logger.Error("usage: server check-business-config | {review-status|check-review|preview-review|preview-approval|prepare-approval|prepare-approval-files|retry-approval-files|submit-approval|approval-status|check-approval|abandon-approval|submit-seal|submit-review|retry-writeback|apply-seal-result} <record-id-preparation-id-document-id-or-file>")
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		switch os.Args[1] {
		case "check-business-config":
			err = runBusinessCheck(ctx, cfg, os.Stdout)
		case "preview-review":
			err = runReviewPreview(ctx, cfg, os.Args[2], os.Stdout)
		case "preview-approval":
			err = runApprovalPreview(ctx, cfg, os.Args[2], os.Stdout)
		case "prepare-approval-files":
			err = runApprovalFilePreparation(ctx, cfg, os.Args[2], false, os.Stdout)
		case "prepare-approval":
			err = runApprovalRequestPreparation(ctx, cfg, os.Args[2], os.Stdout)
		case "submit-approval":
			err = runApprovalSubmission(ctx, cfg, os.Args[2], os.Stdout)
		case "approval-status":
			err = runApprovalStatus(ctx, cfg, os.Args[2], os.Stdout)
		case "check-approval":
			err = runApprovalReconciliation(ctx, cfg, os.Args[2], os.Stdout)
		case "abandon-approval":
			err = runApprovalAbandonment(ctx, cfg, os.Args[2], os.Stdout)
		case "retry-approval-files":
			err = runApprovalFilePreparation(ctx, cfg, os.Args[2], true, os.Stdout)
		case "review-status":
			err = runReviewInspection(ctx, cfg, os.Args[2], false, os.Stdout)
		case "check-review":
			err = runReviewInspection(ctx, cfg, os.Args[2], true, os.Stdout)
		case "retry-writeback":
			err = runReviewWriteback(ctx, cfg, os.Args[2], os.Stdout)
		case "submit-seal":
			err = runSealSubmit(ctx, cfg, os.Args[2], logger)
		case "submit-review":
			err = runReviewSubmit(ctx, cfg, os.Args[2], cfg.ReviewProvider, true, logger)
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
		logger.Info("business profile selected", "profile", cfg.Business.Name, "config_file", cfg.BusinessConfigFile,
			"source_base", cfg.ReceiptBaseToken, "source_table", cfg.ReceiptTableID, "ledger_table", cfg.ReceiptLedgerTableID,
			"receipt_provider", cfg.ReceiptProvider, "review_provider", cfg.ReviewProvider, "review_trigger", cfg.ReviewTriggerMode,
			"include_transactions", cfg.Business.Review.IncludeTransactions)
		logger.Info("review change settings", "resubmit_on_detail_change", cfg.Business.Review.ResubmitOnDetailChange,
			"resubmit_on_source_change", cfg.Business.Review.ResubmitOnSourceChange,
			"change_debounce", cfg.Business.Review.ChangeDebounce)
	}
	server := httpserver.New(cfg.HTTPAddr, logger, version.Version)
	server.Register("POST /seal/callback/mock", seal.MockCallback(logger))

	var reviewService *appreview.Service
	if cfg.SealCallbackToken != "" {
		reviewService, err = newReviewDeliveryService(cfg)
		if err != nil {
			logger.Error("configure Seal callback", "error", err)
			os.Exit(1)
		}
		server.Register("POST /seal/callback/{token}", seal.CallbackHandler(cfg.SealCallbackToken, reviewService))
	}
	if reviewService == nil && len(cfg.ReviewResultFieldIDs) > 0 {
		reviewService, err = newReviewDeliveryService(cfg)
		if err != nil {
			logger.Error("configure review writeback", "error", err)
			os.Exit(1)
		}
	}
	var automaticReview *appreview.Automatic
	var sourceChanges *appreview.SourceChanges
	if cfg.ReviewTriggerMode == "after_recognition" {
		// Automatic submissions follow REVIEW_PROVIDER independently of a Seal
		// callback receiver that may still finish older in-flight Seal requests.
		submitter, err := newReviewService(cfg, cfg.ReviewProvider, true)
		if err != nil {
			logger.Error("configure automatic review provider", "error", err)
			os.Exit(1)
		}
		triggers, storeErr := state.NewFiles(cfg.StateDir)
		if storeErr != nil {
			logger.Error("configure automatic review state", "error", storeErr)
			os.Exit(1)
		}
		automaticReview, err = appreview.NewAutomatic(submitter, triggers, "feishu:"+cfg.ReceiptBaseToken+":"+cfg.ReceiptTableID, logger)
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
	var feishuListener *events.Listener
	var receiptFlow *recognition.Processor
	var attachmentClient *base.AttachmentClient
	if cfg.FeishuEnabled() {
		sink := events.LoggingSink(logger, cfg.FeishuLogRawEvents)
		if cfg.ReceiptProvider != "" {
			var recognizer invoice.Recognizer
			switch cfg.ReceiptProvider {
			case "anyreceipt":
				recognizer, err = newAnyreceipt(cfg.AnyreceiptAPIKey)
			case "model":
				recognizer, err = newModel(cfg.ReceiptModelAPIKey, cfg.ReceiptModelBaseURL, cfg.ReceiptModelName)
			default:
				err = errors.New("unsupported RECEIPT_PROVIDER")
			}
			if err != nil {
				logger.Error("configure receipt recognizer", "error", err)
				os.Exit(1)
			}
			attachmentClient = base.NewAttachmentClient(cfg.FeishuAppID, cfg.FeishuAppSecret)
			resultHandler := recognition.ResultHandler(func(ctx context.Context, result recognition.Result) error {
				logger.InfoContext(ctx, "receipt recognized", "trigger", result.Trigger, "record_id", result.RecordID, "file_name", result.FileName, "output_fields", len(result.Recognition.Outputs))
				return nil
			})
			if cfg.ReceiptLedgerTableID != "" {
				ledgerHandler, ledgerErr := invoiceledger.New(invoiceledger.Config{BaseToken: cfg.ReceiptBaseToken, SourceTableID: cfg.ReceiptTableID, SourceDetailFieldID: cfg.ReceiptSourceDetailFieldID, TableID: cfg.ReceiptLedgerTableID, Fields: cfg.ReceiptLedgerFieldIDs}, base.NewLedgerClient(cfg.FeishuAppID, cfg.FeishuAppSecret), logger)
				if ledgerErr != nil {
					logger.Error("configure invoice ledger", "error", ledgerErr)
					os.Exit(1)
				}
				resultHandler = func(ctx context.Context, result recognition.Result) error {
					if err := ledgerHandler.Handle(ctx, result); err != nil {
						return err
					}
					if automaticReview != nil {
						return automaticReview.NotifyRecognition(ctx, result.RecordID)
					}
					return nil
				}
			}
			receiptFlow, err = recognition.New(recognition.Config{BaseToken: cfg.ReceiptBaseToken, TableID: cfg.ReceiptTableID, FieldID: cfg.ReceiptFieldID}, attachmentClient, recognizer, resultHandler, logger)
			if err != nil {
				logger.Error("configure receipt flow", "error", err)
				os.Exit(1)
			}
			checkpoints, cacheErr := state.NewFiles(cfg.StateDir)
			if cacheErr != nil {
				logger.Error("configure receipt state", "error", cacheErr)
				os.Exit(1)
			}
			scopeData, _ := json.Marshal(struct {
				Base, Table, Field, Provider, Model, Endpoint, Ledger string
				Fields                                                map[string]string
			}{cfg.ReceiptBaseToken, cfg.ReceiptTableID, cfg.ReceiptFieldID, cfg.ReceiptProvider, cfg.ReceiptModelName, cfg.ReceiptModelBaseURL, cfg.ReceiptLedgerTableID, cfg.ReceiptLedgerFieldIDs})
			lease, leaseErr := checkpoints.AcquireWorker(cfg.ReceiptBaseToken + ":" + cfg.ReceiptTableID + ":" + cfg.ReceiptFieldID)
			if leaseErr != nil {
				logger.Error("claim receipt worker", "error", leaseErr)
				os.Exit(1)
			}
			defer lease.Close()
			scopeHash := sha256.Sum256(scopeData)
			receiptFlow.WithCheckpoints(checkpoints, hex.EncodeToString(scopeHash[:]))
			if cfg.ReceiptTriggerMode != "poll" {
				logEvent := sink
				attachmentSink := events.NewAttachmentSink(recognition.Config{BaseToken: cfg.ReceiptBaseToken, TableID: cfg.ReceiptTableID, FieldID: cfg.ReceiptFieldID}, receiptFlow).Sink
				sink = func(ctx context.Context, event events.Event) error {
					if err := logEvent(ctx, event); err != nil {
						return err
					}
					return attachmentSink(ctx, event)
				}
			}
		}
		if automaticReview != nil && cfg.Business != nil && cfg.Business.Review.ResubmitOnDetailChange {
			fieldIDs := []string{cfg.ReceiptFieldID, cfg.ReceiptSourceDetailFieldID}
			for _, id := range cfg.ReviewContextFieldIDs {
				fieldIDs = append(fieldIDs, id)
			}
			if cfg.Business.Review.IncludeTransactions {
				fieldIDs = append(fieldIDs, cfg.Business.Tables.ReimbursementDetails.Fields["transaction_relation"])
			}
			changeSink, changeErr := events.NewReviewChangeSink(cfg.ReceiptBaseToken, cfg.ReceiptTableID, fieldIDs, automaticReview)
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
			var sourceSink *events.ReviewSourceChangeSink
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
		if cfg.ReceiptProvider == "" || cfg.ReceiptTriggerMode != "poll" || reviewChangesEnabled(cfg) {
			feishuListener, err = events.New(
				cfg.FeishuAppID,
				cfg.FeishuAppSecret,
				cfg.FeishuEventType,
				sink,
				logger,
			)
			if err != nil {
				logger.Error("create Feishu listener", "error", err)
				os.Exit(1)
			}
		}
	} else {
		logger.Info("Feishu long connection disabled", "reason", "credentials not configured")
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
		if cfg.ReceiptTriggerMode != "event" {
			go receiptFlow.Poll(ctx, attachmentClient, cfg.ReceiptPollInterval, cfg.ReceiptPollStartup == "process")
		}
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server started", "addr", cfg.HTTPAddr, "version", version.Version)
		serverErr <- server.ListenAndServe()
	}()

	var feishuErr <-chan error
	if feishuListener != nil {
		listenerErr := make(chan error, 1)
		feishuErr = listenerErr
		go func() {
			logger.Info("starting Feishu long connection", "event_type", cfg.FeishuEventType)
			listenerErr <- feishuListener.Start(ctx)
		}()
	}

	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("graceful shutdown failed", "error", err)
		}
		if feishuListener != nil {
			if err := feishuListener.CloseAndWait(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
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
