// Package review coordinates a completed ledger record with the optional Seal
// audit path. It depends on interfaces, not on Feishu HTTP or OCR providers.
// Shared orchestration now lives in app/review; this package owns Seal transport.
package review

import (
	"context"
	"errors"
	"fmt"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal/mapper"
	"sort"
)

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

// Submit reads every attachment and its written ledger result before uploading
// originals. One reimbursement record becomes one Seal document with many invoices.
// The shared app/review Service implements Submit; Review below adapts the frozen input.
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
	headerFields := []seal.DocumentField{
		{Key: "detail_id", Label: "报销明细ID", Type: "TEXT", Value: batch.DocumentSN, Comments: "来源：个人报销明细.明细ID"},
	}
	if category := request.Context["expense_category"]; category != "" {
		headerFields = append(headerFields, seal.DocumentField{Key: "expense_category", Label: "报销费用类目", Type: "TEXT", Value: category, Comments: "来源：个人报销明细.费用类目"})
	}
	if reason := request.Context["expense_reason"]; reason != "" {
		headerFields = append(headerFields, seal.DocumentField{Key: "expense_reason", Label: "消费事由", Type: "TEXT", Value: reason, Comments: "来源：个人报销明细.消费事由"})
	}
	if request.Transactions != nil && len(request.Transactions.Transactions) > 0 {
		tx := request.Transactions.Transactions[0]
		if tx.OriginalAmount != "" {
			amt := tx.OriginalAmount
			if tx.OriginalCurrency != "" {
				amt = tx.OriginalCurrency + " " + amt
			}
			headerFields = append(headerFields, seal.DocumentField{Key: "expense_amount", Label: "报销原币金额", Type: "TEXT", Value: amt, Comments: "来源：交易流水表.交易金额(原币)"})
		}
		if tx.OccurredAt != "" {
			headerFields = append(headerFields, seal.DocumentField{Key: "expense_date", Label: "消费日期", Type: "TEXT", Value: tx.OccurredAt, Comments: "来源：交易流水表.交易时间"})
		}
		if tx.TransactionID != "" {
			headerFields = append(headerFields, seal.DocumentField{Key: "transaction_id", Label: "交易流水号", Type: "TEXT", Value: tx.TransactionID, Comments: "来源：交易流水表.合思交易流水号"})
		}
		if tx.Merchant != "" {
			headerFields = append(headerFields, seal.DocumentField{Key: "merchant_name", Label: "商户名称", Type: "TEXT", Value: tx.Merchant, Comments: "来源：交易流水表.商户名称"})
		}
		if tx.BookedAmountCNY != "" {
			headerFields = append(headerFields, seal.DocumentField{Key: "settled_amount_cny", Label: "CNY结算金额", Type: "TEXT", Value: "CNY " + tx.BookedAmountCNY, Comments: "来源：交易流水表.记账金额(CNY)"})
		}
	}
	payload.Fields = append(headerFields, payload.Fields...)

	transactionFields, err := mapper.TransactionFields(request.Transactions)
	if err != nil {
		return core.Submission{}, err
	}
	payload.Fields = append(payload.Fields, transactionFields...)
	keys := make([]string, 0, len(request.Context))
	for key := range request.Context {
		if key != "expense_category" && key != "expense_reason" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		payload.Fields = append(payload.Fields, seal.DocumentField{Key: key, Label: key, Type: "TEXT", Value: request.Context[key], Comments: "来源：个人报销明细上下文"})
	}
	if request.Revision != "" {
		payload.Fields = append(payload.Fields, seal.DocumentField{Key: "bridge_revision", Label: "业务数据版本", Type: "TEXT", Value: request.Revision, Comments: "来源：数据快照哈希指纹"})
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
