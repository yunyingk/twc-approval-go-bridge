package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// runReviewPreview exercises the same source and revision calculation as submit.
// It never begins an attempt, calls a reviewer or writes a business record.
func runReviewPreview(ctx context.Context, cfg config.Config, recordID string, output io.Writer) error {
	service, err := newReviewService(cfg)
	if err != nil {
		return err
	}
	request, err := service.Prepare(ctx, recordID)
	if err != nil {
		return err
	}
	type receipt struct {
		SourceKey      string         `json:"source_key"`
		LedgerRecordID string         `json:"ledger_record_id"`
		Facts          *invoice.Facts `json:"facts"`
		CandidateCount int            `json:"candidate_count"`
	}
	receipts := make([]receipt, 0, len(request.Document.Invoices))
	for _, item := range request.Document.Invoices {
		receipts = append(receipts, receipt{item.Facts.SourceKey, item.LedgerRecordID, item.Recognition.Facts, len(item.Candidates)})
	}
	return json.NewEncoder(output).Encode(struct {
		LogicalID    string                    `json:"logical_id"`
		Revision     string                    `json:"revision"`
		Provider     string                    `json:"provider"`
		Context      map[string]string         `json:"context"`
		Transactions *core.TransactionEvidence `json:"transactions,omitempty"`
		Invoices     []receipt                 `json:"invoices"`
	}{request.LogicalID, request.Revision, request.Provider, request.Context, request.Transactions, receipts})
}
