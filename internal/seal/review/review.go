// Package review coordinates a completed ledger record with the optional Seal
// audit path. It depends on interfaces, not on Feishu HTTP or OCR providers.
// Shared orchestration now lives in app/review; this package owns Seal transport.
package review

import (
	"context"
	"errors"
	"fmt"
	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal/mapper"
	"sort"
)

type File = core.File
type Detail = core.Detail
type LedgerEntry = core.LedgerEntry
type Source = app.Source
type Result = core.Submission

type SealGateway interface {
	UploadAttachment(context.Context, seal.Attachment) (seal.UploadResponse, error)
	SubmitDocument(context.Context, seal.DocumentRequest) (seal.SubmitResponse, error)
}
type Gateway struct{ client SealGateway }

func NewGateway(client SealGateway) (*Gateway, error) {
	if client == nil {
		return nil, fmt.Errorf("Seal gateway is required")
	}
	return &Gateway{client: client}, nil
}

// Service preserves the explicit legacy command while using the shared use case.
type Service struct{ *app.Service }

func New(source Source, client SealGateway) (*Service, error) {
	gateway, err := NewGateway(client)
	if err != nil {
		return nil, err
	}
	service, err := app.New(source, gateway, app.Options{Provider: "seal"})
	if err != nil {
		return nil, err
	}
	return &Service{service}, nil
}

// Submit reads every attachment and its written ledger result before uploading
// originals. One reimbursement record becomes one Seal document with many invoices.
// The shared Service implements Submit; Review below only adapts the frozen input.
func (g *Gateway) Review(ctx context.Context, request core.Request) (core.Submission, error) {
	batch := request.Document
	uploads := make(map[string]seal.UploadResponse, len(batch.Invoices))
	for _, invoice := range batch.Invoices {
		attachment := invoice.Attachment
		upload, err := g.client.UploadAttachment(ctx, seal.Attachment{Name: attachment.Name,
			ContentType: attachment.ContentType, Data: attachment.Data})
		if err != nil {
			return core.Submission{}, fmt.Errorf("upload original receipt to Seal: %w", err)
		}
		uploads[invoice.FileToken] = upload
	}
	payload, err := mapper.MapBatch(batch, uploads)
	if err != nil {
		return core.Submission{}, err
	}
	transactionFields, err := mapper.TransactionFields(request.Transactions)
	if err != nil {
		return core.Submission{}, err
	}
	payload.Fields = append(payload.Fields, transactionFields...)
	keys := make([]string, 0, len(request.Context))
	for key := range request.Context {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		payload.Fields = append(payload.Fields, seal.DocumentField{Key: key, Label: key, Type: "TEXT", Value: request.Context[key]})
	}
	if request.Revision != "" {
		payload.Fields = append(payload.Fields, seal.DocumentField{Key: "bridge_revision", Label: "业务数据版本", Type: "TEXT", Value: request.Revision})
	}
	attachmentFields := 0
	for _, field := range payload.Fields {
		if field.Type == "ATTACHMENT" {
			attachmentFields++
		}
	}
	response, err := g.client.SubmitDocument(ctx, payload)
	if err != nil {
		var status *seal.HTTPStatusError
		if errors.As(err, &status) && status.StatusCode >= 400 && status.StatusCode < 500 && status.StatusCode != 408 && status.StatusCode != 429 {
			return core.Submission{}, fmt.Errorf("%w: %w", core.ErrRequestRejected, err)
		}
		return core.Submission{}, err
	}
	return core.Submission{DocumentID: batch.DocumentID, Status: "pending", AttachmentCount: len(batch.Invoices), AttachmentFields: attachmentFields, StructuredInvoices: len(payload.Invoices), DuplicateCandidates: len(batch.Findings), AcceptedInvoices: len(response.AcceptedInvoices)}, nil
}
