package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

// runSealSubmit is an explicit one-record trigger while approval timing and
// public callbacks are being established. Business logic stays in review.
func runSealSubmit(ctx context.Context, cfg config.Config, recordID string, logger *slog.Logger) error {
	return runReviewSubmit(ctx, cfg, recordID, "seal", false, logger)
}

func newReviewService(cfg config.Config, provider string, versioned bool) (*appreview.Service, error) {
	source, err := base.NewReviewSource(cfg.FeishuAppID, cfg.FeishuAppSecret, cfg.ReceiptBaseToken, cfg.ReceiptTableID, cfg.ReceiptFieldID, cfg.ReceiptSourceDetailFieldID, cfg.ReceiptLedgerTableID, cfg.ReceiptLedgerFieldIDs)
	if err != nil {
		return nil, err
	}
	source.WithContextFields(cfg.ReviewContextFieldIDs)
	options := appreview.Options{Provider: provider, Versioned: versioned}
	var gateway appreview.Reviewer
	switch provider {
	case "seal":
		client, err := seal.NewClient(seal.Config{DocumentURL: cfg.SealDocumentURL, BearerToken: cfg.SealBearerToken}, nil)
		if err != nil {
			return nil, err
		}
		gateway, err = review.NewGateway(client)
		if err != nil {
			return nil, err
		}
		options.ProviderVersion = cfg.SealDocumentURL
	case "model":
		gateway, options.RulesVersion, options.ProviderVersion, err = newModelReview(cfg)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported review provider")
	}
	if versioned {
		store, err := state.NewFiles(cfg.StateDir)
		if err != nil {
			return nil, err
		}
		options.Store = store
		if len(cfg.ReviewResultFieldIDs) > 0 {
			writer, err := base.NewReviewWriter(base.NewLedgerClient(cfg.FeishuAppID, cfg.FeishuAppSecret), cfg.ReceiptBaseToken, cfg.ReceiptTableID, cfg.ReviewResultFieldIDs)
			if err != nil {
				return nil, err
			}
			options.Writer = writer
		}
	}
	return appreview.New(source, gateway, options)
}
func runReviewSubmit(ctx context.Context, cfg config.Config, recordID, provider string, versioned bool, logger *slog.Logger) error {
	service, err := newReviewService(cfg, provider, versioned)
	if err != nil {
		return err
	}
	result, err := service.Submit(ctx, recordID)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "review submitted", "provider", provider, "document_id", result.DocumentID, "status", result.Status, "attachment_count", result.AttachmentCount, "structured_invoices", result.StructuredInvoices, "duplicate_candidates", result.DuplicateCandidates)
	return json.NewEncoder(os.Stdout).Encode(result)
}

// A local operator import for already verified Seal results; no public auth is assumed.
func runSealResult(ctx context.Context, cfg config.Config, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return err
	}
	id, outcome, err := seal.DecodeCallback(raw)
	if err != nil {
		return err
	}
	service, err := newReviewService(cfg, "seal", true)
	if err != nil {
		return err
	}
	return service.Complete(ctx, id, "seal", outcome)
}
