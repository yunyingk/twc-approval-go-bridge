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

func newReviewSource(cfg config.Config) (*base.ReviewSource, error) {
	if cfg.Business == nil {
		return nil, fmt.Errorf("business configuration is required")
	}
	details := cfg.Business.Tables.ReimbursementDetails
	ledger := cfg.Business.Tables.InvoiceLedger
	source, err := base.NewReviewSource(cfg.Feishu.AppID, cfg.Feishu.AppSecret, details.BaseToken, details.TableID, details.Fields["attachments"], details.Fields["ledger_relation"], ledger.TableID, ledger.Fields)
	if err != nil {
		return nil, err
	}
	source.WithContextFields(cfg.Business.Review.ContextFields)
	if cfg.Business.Review.IncludeTransactions {
		table := cfg.Business.Tables.Transactions
		if _, err := source.WithTransactions(base.TransactionConfig{BaseToken: table.BaseToken, TableID: table.TableID,
			RelationFieldID: details.Fields["transaction_relation"], Fields: table.Fields}); err != nil {
			return nil, err
		}
	}
	return source, nil
}

func newReviewService(cfg config.Config) (*appreview.Service, error) {
	source, err := newReviewSource(cfg)
	if err != nil {
		return nil, err
	}
	provider := cfg.ReviewProvider()
	options := appreview.Options{Provider: provider, Versioned: true}
	var gateway appreview.Reviewer
	switch provider {
	case "seal":
		client, err := seal.NewClient(seal.Config{DocumentURL: cfg.Seal.DocumentURL, BearerToken: cfg.Seal.BearerToken}, nil)
		if err != nil {
			return nil, err
		}
		gateway, err = review.NewGateway(client)
		if err != nil {
			return nil, err
		}
		options.ProviderVersion = cfg.Seal.DocumentURL
	case "model":
		gateway, options.RulesVersion, options.ProviderVersion, err = newModelReview(cfg)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported review provider")
	}
	store, err := state.NewFiles(cfg.Runtime.StateDir)
	if err != nil {
		return nil, err
	}
	options.Store = store
	if cfg.Business != nil && len(cfg.Business.Review.ResultFields) > 0 {
		writer, err := base.NewReviewWriter(base.NewLedgerClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret), cfg.Business.Tables.ReimbursementDetails.BaseToken, cfg.Business.Tables.ReimbursementDetails.TableID, cfg.Business.Review.ResultFields)
		if err != nil {
			return nil, err
		}
		options.Writer = writer
	}
	return appreview.New(source, gateway, options)
}

func newReviewDeliveryService(cfg config.Config) (*appreview.Service, error) {
	source, err := newReviewSource(cfg)
	if err != nil {
		return nil, err
	}
	store, err := state.NewFiles(cfg.Runtime.StateDir)
	if err != nil {
		return nil, err
	}
	var writer appreview.Writer
	if cfg.Business != nil && len(cfg.Business.Review.ResultFields) > 0 {
		writer, err = base.NewReviewWriter(base.NewLedgerClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret), cfg.Business.Tables.ReimbursementDetails.BaseToken, cfg.Business.Tables.ReimbursementDetails.TableID, cfg.Business.Review.ResultFields)
		if err != nil {
			return nil, err
		}
	}
	return appreview.NewDelivery(source, store, writer)
}
func runReviewSubmit(ctx context.Context, cfg config.Config, recordID string, logger *slog.Logger) error {
	service, err := newReviewService(cfg)
	if err != nil {
		return err
	}
	result, err := service.Submit(ctx, recordID)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "review submitted", "provider", cfg.ReviewProvider(), "document_id", result.DocumentID, "status", result.Status, "attachment_count", result.AttachmentCount, "structured_invoices", result.StructuredInvoices, "duplicate_candidates", result.DuplicateCandidates)
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
	service, err := newReviewDeliveryService(cfg)
	if err != nil {
		return err
	}
	return service.Complete(ctx, id, "seal", outcome)
}
