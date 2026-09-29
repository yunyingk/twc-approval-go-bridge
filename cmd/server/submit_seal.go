package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal/review"
)

// runSealSubmit is an explicit one-record trigger while approval timing and
// public callbacks are being established. Business logic stays in review.
func runSealSubmit(ctx context.Context, cfg config.Config, recordID string, logger *slog.Logger) error {
	if cfg.SealDocumentURL == "" || cfg.SealBearerToken == "" {
		return fmt.Errorf("SEAL_DOCUMENT_URL and SEAL_BEARER_TOKEN are required")
	}
	source, err := base.NewReviewSource(cfg.FeishuAppID, cfg.FeishuAppSecret,
		cfg.ReceiptBaseToken, cfg.ReceiptTableID, cfg.ReceiptFieldID,
		cfg.ReceiptSourceDetailFieldID, cfg.ReceiptLedgerTableID, cfg.ReceiptLedgerFieldIDs)
	if err != nil {
		return err
	}
	client, err := seal.NewClient(seal.Config{DocumentURL: cfg.SealDocumentURL, BearerToken: cfg.SealBearerToken}, nil)
	if err != nil {
		return err
	}
	service, err := review.New(source, client)
	if err != nil {
		return err
	}
	result, err := service.Submit(ctx, recordID)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "Seal reimbursement submitted", "document_id", result.DocumentID,
		"attachments_uploaded", result.AttachmentCount, "attachment_fields", result.AttachmentFields,
		"structured_invoices", result.StructuredInvoices,
		"duplicate_candidates", result.DuplicateCandidates, "accepted_invoices", len(result.Seal.AcceptedInvoices))
	return nil
}
