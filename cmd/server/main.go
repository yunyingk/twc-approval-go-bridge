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
		if len(os.Args) != 3 {
			logger.Error("usage: server {submit-seal|submit-review|apply-seal-result} <record-id-or-file>")
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		switch os.Args[1] {
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
			logger.Error("review command failed", "error", err)
			os.Exit(1)
		}
		return
	}
	server := httpserver.New(cfg.HTTPAddr, logger, version.Version)
	server.Register("POST /seal/callback/mock", seal.MockCallback(logger))

	var reviewService *appreview.Service
	var reviewServiceProvider string
	if cfg.SealCallbackToken != "" {
		reviewService, err = newReviewService(cfg, "seal", true)
		if err != nil {
			logger.Error("configure Seal callback", "error", err)
			os.Exit(1)
		}
		server.Register("POST /seal/callback/{token}", seal.CallbackHandler(cfg.SealCallbackToken, reviewService))
		reviewServiceProvider = "seal"
	}
	if reviewService == nil && len(cfg.ReviewResultFieldIDs) > 0 {
		reviewService, err = newReviewService(cfg, cfg.ReviewProvider, true)
		if err != nil {
			logger.Error("configure review writeback", "error", err)
			os.Exit(1)
		}
		reviewServiceProvider = cfg.ReviewProvider
	}
	var automaticReview *appreview.Automatic
	if cfg.ReviewTriggerMode == "after_recognition" {
		// Automatic submissions follow REVIEW_PROVIDER independently of a Seal
		// callback receiver that may still finish older in-flight Seal requests.
		submitter := reviewService
		if submitter == nil || cfg.ReviewProvider != reviewServiceProvider {
			submitter, err = newReviewService(cfg, cfg.ReviewProvider, true)
			if err != nil {
				logger.Error("configure automatic review provider", "error", err)
				os.Exit(1)
			}
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
		if cfg.ReceiptProvider == "" || cfg.ReceiptTriggerMode != "poll" {
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
