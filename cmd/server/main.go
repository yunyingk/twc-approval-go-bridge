package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/httpserver"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/flow"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/ledger"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/model"
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
	server := httpserver.New(cfg.HTTPAddr, logger, version.Version)

	var feishuListener *events.Listener
	var receiptFlow *flow.Processor
	var attachmentClient *feishu.AttachmentClient
	if cfg.FeishuEnabled() {
		sink := events.LoggingSink(logger, cfg.FeishuLogRawEvents)
		if cfg.ReceiptProvider != "" {
			var recognizer receipt.Recognizer
			switch cfg.ReceiptProvider {
			case "anyreceipt":
				recognizer, err = newAnyreceipt(cfg.AnyreceiptAPIKey)
			case "model":
				recognizer, err = model.New(cfg.ReceiptModelAPIKey, cfg.ReceiptModelBaseURL, cfg.ReceiptModelName)
			default:
				err = errors.New("unsupported RECEIPT_PROVIDER")
			}
			if err != nil {
				logger.Error("configure receipt recognizer", "error", err)
				os.Exit(1)
			}
			attachmentClient = feishu.NewAttachmentClient(cfg.FeishuAppID, cfg.FeishuAppSecret)
			resultHandler := flow.ResultHandler(func(ctx context.Context, result flow.Result) error {
				logger.InfoContext(ctx, "receipt recognized", "trigger", result.Trigger, "record_id", result.RecordID, "file_name", result.FileName, "output_fields", len(result.Recognition.Outputs))
				return nil
			})
			if cfg.ReceiptLedgerTableID != "" {
				ledgerHandler, ledgerErr := ledger.New(ledger.Config{BaseToken: cfg.ReceiptBaseToken, SourceTableID: cfg.ReceiptTableID, SourceDetailFieldID: cfg.ReceiptSourceDetailFieldID, TableID: cfg.ReceiptLedgerTableID, Fields: cfg.ReceiptLedgerFieldIDs}, feishu.NewLedgerClient(cfg.FeishuAppID, cfg.FeishuAppSecret), logger)
				if ledgerErr != nil {
					logger.Error("configure invoice ledger", "error", ledgerErr)
					os.Exit(1)
				}
				resultHandler = ledgerHandler.Handle
			}
			receiptFlow, err = flow.New(flow.Config{BaseToken: cfg.ReceiptBaseToken, TableID: cfg.ReceiptTableID, FieldID: cfg.ReceiptFieldID}, attachmentClient, recognizer, resultHandler, logger)
			if err != nil {
				logger.Error("configure receipt flow", "error", err)
				os.Exit(1)
			}
			if cfg.ReceiptTriggerMode != "poll" {
				sink = receiptFlow.Sink
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
