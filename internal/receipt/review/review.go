// Package review coordinates a completed ledger record with the optional Seal
// audit path. It depends on interfaces, not on Feishu HTTP or OCR providers.
package review

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/aggregate"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/sealmapper"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
)

type File struct {
	Token      string
	Attachment receipt.Attachment
}

type Detail struct {
	DocumentID string
	DocumentSN string
	RecordID   string
	StartTime  time.Time
	Files      []File
}

type LedgerEntry struct {
	RecordID    string
	Recognition receipt.Recognition
	Facts       dupcheck.Invoice
}

type Source interface {
	ReadDetail(context.Context, string) (Detail, error)
	ReadLedgerEntry(context.Context, string) (LedgerEntry, error)
	FindInvoiceCandidates(context.Context, string) ([]dupcheck.Invoice, error)
}

type SealGateway interface {
	UploadAttachment(context.Context, seal.Attachment) (seal.UploadResponse, error)
	SubmitDocument(context.Context, seal.DocumentRequest) (seal.SubmitResponse, error)
}

type Service struct {
	source Source
	seal   SealGateway
}

func New(source Source, gateway SealGateway) (*Service, error) {
	if source == nil || gateway == nil {
		return nil, fmt.Errorf("review source and Seal gateway are required")
	}
	return &Service{source: source, seal: gateway}, nil
}

type Result struct {
	DocumentID          string
	AttachmentCount     int
	AttachmentFields    int
	StructuredInvoices  int
	DuplicateCandidates int
	Seal                seal.SubmitResponse
}

// Submit reads every attachment and its written ledger result before uploading
// originals. One reimbursement record becomes one Seal document with many invoices.
func (s *Service) Submit(ctx context.Context, recordID string) (Result, error) {
	if strings.TrimSpace(recordID) == "" {
		return Result{}, fmt.Errorf("source record ID is required")
	}
	detail, err := s.source.ReadDetail(ctx, recordID)
	if err != nil {
		return Result{}, err
	}
	if detail.RecordID != recordID || len(detail.Files) == 0 {
		return Result{}, fmt.Errorf("source detail does not match record or has no attachments")
	}
	invoices := make([]aggregate.Invoice, 0, len(detail.Files))
	for _, file := range detail.Files {
		key := recordID + ":" + file.Token
		entry, err := s.source.ReadLedgerEntry(ctx, key)
		if err != nil {
			return Result{}, fmt.Errorf("load invoice ledger for attachment: %w", err)
		}
		var candidates []dupcheck.Invoice
		if entry.Facts.Number != "" {
			candidates, err = s.source.FindInvoiceCandidates(ctx, entry.Facts.Number)
			if err != nil {
				return Result{}, fmt.Errorf("search invoice ledger: %w", err)
			}
		}
		invoices = append(invoices, aggregate.Invoice{FileToken: file.Token, LedgerRecordID: entry.RecordID,
			Attachment: file.Attachment, Recognition: entry.Recognition, Facts: entry.Facts, Candidates: candidates})
	}
	batch, err := aggregate.Build(detail.DocumentID, detail.DocumentSN, recordID, detail.StartTime, invoices)
	if err != nil {
		return Result{}, err
	}
	uploads := make(map[string]seal.UploadResponse, len(batch.Invoices))
	for _, invoice := range batch.Invoices {
		attachment := invoice.Attachment
		upload, err := s.seal.UploadAttachment(ctx, seal.Attachment{Name: attachment.Name,
			ContentType: attachment.ContentType, Data: attachment.Data})
		if err != nil {
			return Result{}, fmt.Errorf("upload original receipt to Seal: %w", err)
		}
		uploads[invoice.FileToken] = upload
	}
	payload, err := sealmapper.MapBatch(batch, uploads)
	if err != nil {
		return Result{}, err
	}
	attachmentFields := 0
	for _, field := range payload.Fields {
		if field.Type == "ATTACHMENT" {
			attachmentFields++
		}
	}
	response, err := s.seal.SubmitDocument(ctx, payload)
	if err != nil {
		return Result{}, err
	}
	return Result{DocumentID: batch.DocumentID, AttachmentCount: len(batch.Invoices), AttachmentFields: attachmentFields,
		StructuredInvoices: len(payload.Invoices), DuplicateCandidates: len(batch.Findings), Seal: response}, nil
}
