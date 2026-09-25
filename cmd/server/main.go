package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/httpserver"
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
	if cfg.FeishuEnabled() {
		feishuListener, err = events.New(
			cfg.FeishuAppID,
			cfg.FeishuAppSecret,
			cfg.FeishuEventType,
			events.LoggingSink(logger, cfg.FeishuLogRawEvents),
			logger,
		)
		if err != nil {
			logger.Error("create Feishu listener", "error", err)
			os.Exit(1)
		}
	} else {
		logger.Info("Feishu long connection disabled", "reason", "credentials not configured")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
