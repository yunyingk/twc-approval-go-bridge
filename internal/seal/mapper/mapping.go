// Package mapper turns recognized receipts into Seal AI document requests.
// It is deliberately separate from both the Feishu adapter and the generic Seal client.
package mapper

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receiptcompat"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
)

// Input contains the facts available after OCR and a successful Seal upload.
// StartTime is supplied by the caller so retries and tests can control it.
type Input struct {
	DocumentID  string
	DocumentSN  string
	RecordID    string
	FileToken   string
	FileName    string
	Recognition invoice.Recognition
	Upload      seal.UploadResponse
	StartTime   time.Time
}

// Map maps one receipt into a caller-identified reimbursement document.
// The caller can combine fields and invoices from several receipts before
// submitting that document. A structured invoice is included only if all of
// its required OCR facts and uploaded evidence are present.
func Map(input Input) (seal.DocumentRequest, error) {
	if strings.TrimSpace(input.DocumentID) == "" || strings.TrimSpace(input.DocumentSN) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("Seal document ID and SN are required")
	}
	if strings.TrimSpace(input.RecordID) == "" || strings.TrimSpace(input.FileToken) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("receipt source record ID and file token are required")
	}
	if strings.TrimSpace(input.Upload.AttachmentID) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("uploaded receipt attachment ID is required")
	}
	if input.StartTime.IsZero() || input.StartTime.Unix() <= 0 {
		return seal.DocumentRequest{}, fmt.Errorf("document start time must be after Unix epoch")
	}
	input.Recognition = receiptcompat.Normalize(input.Recognition)
	facts := input.Recognition.Facts
	sourceID := input.RecordID + ":" + input.FileToken
	document := seal.DocumentRequest{
		DocumentID: input.DocumentID,
		DocumentSN: input.DocumentSN,
		StartTime:  input.StartTime.Unix(),
		Fields: []seal.DocumentField{
			{Key: "source_record_id", Label: "报销明细记录 ID", Type: "TEXT", Value: input.RecordID},
			{Key: "source_attachment_token", Label: "飞书附件 Token", Type: "TEXT", Value: input.FileToken},
		},
	}
	addText := func(key, label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			document.Fields = append(document.Fields, seal.DocumentField{Key: key, Label: label, Type: "TEXT", Value: value})
		}
	}
	addText("attachment_name", "附件文件名", input.FileName)
	if !input.Upload.Valid() {
		return seal.DocumentRequest{}, fmt.Errorf("Seal attachment upload has not completed successfully")
	}
	document.Fields = append(document.Fields, seal.DocumentField{
		Key: "receipt_attachment", Label: "发票附件", Type: "ATTACHMENT", Value: []seal.AttachmentInfo{input.Upload.Attachment},
	})
	addText("receipt_title", "发票摘要", facts.Title)
	addText("receipt_number", "发票号", facts.Number)
	addText("receipt_type", "票据类型", facts.ReceiptType)
	addText("receipt_business_category", "业务分类", facts.BusinessCategory)
	addText("receipt_seller", "开票方", facts.Seller)
	addText("receipt_buyer", "购买方", facts.Buyer)
	addText("receipt_currency", "币种", facts.Currency)
	addText("receipt_total", "含税金额", facts.Total)
	addText("receipt_tax", "税额", facts.Tax)
	addText("receipt_date", "开票日期", facts.IssueDate)
	addText("receipt_country", "国家", facts.Country)
	addText("receipt_summary", "AI 消费概要", input.Recognition.Summary)

	if invoice, ok := mapInvoice(input, sourceID); ok {
		document.Invoices = []seal.ExternalInvoice{invoice}
	}
	return document, nil
}

// MapBatch adapts a provider-independent, multi-invoice reimbursement to one
// Seal document. Every original must already have been uploaded to Seal.
func MapBatch(batch aggregate.Document, uploads map[string]seal.UploadResponse) (seal.DocumentRequest, error) {
	result := seal.DocumentRequest{DocumentID: batch.DocumentID, DocumentSN: batch.DocumentSN,
		StartTime: batch.StartTime.Unix(), Fields: []seal.DocumentField{}}
	for index, item := range batch.Invoices {
		upload, ok := uploads[item.FileToken]
		if !ok || upload.AttachmentID == "" {
			return seal.DocumentRequest{}, fmt.Errorf("Seal upload missing for attachment %d", index+1)
		}
		part, err := Map(Input{DocumentID: batch.DocumentID, DocumentSN: batch.DocumentSN,
			RecordID: batch.RecordID, FileToken: item.FileToken, FileName: item.Attachment.Name,
			Recognition: item.Recognition, Upload: upload, StartTime: batch.StartTime})
		if err != nil {
			return seal.DocumentRequest{}, err
		}
		prefix := fmt.Sprintf("invoice_%02d_", index+1)
		for _, field := range part.Fields {
			field.Key = prefix + field.Key
			result.Fields = append(result.Fields, field)
		}
		if len(item.Recognition.Raw) > 0 {
			result.Fields = append(result.Fields, seal.DocumentField{Key: prefix + "ocr_raw_json", Label: "完整 OCR 结果", Type: "TEXT", Value: string(item.Recognition.Raw)})
		}
		if len(part.Invoices) == 0 {
			result.Fields = append(result.Fields, seal.DocumentField{Key: prefix + "data_quality", Label: "票据数据完整性", Type: "TEXT",
				Value: "结构化发票必填事实不足，请结合完整 OCR 结果和原件人工核对"})
		}
		result.Invoices = append(result.Invoices, part.Invoices...)
	}
	for index, finding := range batch.Findings {
		var prior dupcheck.Invoice
		for _, item := range batch.Invoices {
			if item.Facts.SourceKey != finding.CurrentSourceKey {
				continue
			}
			for _, candidate := range item.Candidates {
				if candidate.RecordID == finding.PriorRecordID && candidate.SourceKey == finding.PriorSourceKey {
					prior = candidate
					break
				}
			}
		}
		// Supply facts as evidence; a candidate is not proof of prior reimbursement.
		evidence, err := json.Marshal(struct {
			CurrentSourceKey string `json:"current_source_key"`
			PriorRecordID    string `json:"prior_record_id"`
			PriorSourceKey   string `json:"prior_source_key"`
			Code             string `json:"code"`
			Number           string `json:"invoice_number"`
			Seller           string `json:"seller"`
			Type             string `json:"receipt_type"`
			IssueDate        string `json:"issue_date"`
			Total            string `json:"total_amount"`
			Currency         string `json:"currency"`
		}{finding.CurrentSourceKey, finding.PriorRecordID, finding.PriorSourceKey, finding.Code,
			prior.Number, prior.Seller, prior.Type, prior.IssueDate, prior.Total, prior.Currency})
		if err != nil {
			return seal.DocumentRequest{}, fmt.Errorf("encode duplicate candidate evidence: %w", err)
		}
		result.Fields = append(result.Fields, seal.DocumentField{
			Key: fmt.Sprintf("duplicate_candidate_%02d", index+1), Label: "台账查重候选", Type: "TEXT",
			Value: string(evidence),
		})
	}
	result.Fields = append(result.Fields, seal.DocumentField{Key: "duplicate_evidence_scope", Label: "查重证据范围", Type: "TEXT",
		Value: "候选来自按当前票号查询的来源记录；缺少票号时未执行查询。候选仅用于核对，不证明已报销或已结算；没有候选不证明不存在重复。检索未覆盖无票号、不同票号、其他来源或所有历史费用，未提供审批与结算状态。"})
	return result, nil
}

func mapInvoice(input Input, sourceID string) (seal.ExternalInvoice, bool) {
	facts := receiptcompat.Normalize(input.Recognition).Facts
	number := facts.Number
	currency := strings.ToUpper(facts.Currency)
	total, totalOK := amount(facts.Total)
	seller := facts.Seller
	buyer := facts.Buyer
	evidenceID := strings.TrimSpace(input.Upload.AttachmentID)
	if number == "" || !currencyCode(currency) || !totalOK || seller == "" || buyer == "" || evidenceID == "" {
		return seal.ExternalInvoice{}, false
	}
	invoice := seal.ExternalInvoice{
		Kind:                 "invoice",
		SourceInvoiceID:      sourceID,
		InvoiceType:          "overseas",
		InvoiceNumber:        number,
		CurrencyCode:         currency,
		TotalAmount:          total,
		Seller:               seal.InvoiceParty{Name: seller},
		Buyer:                seal.InvoiceParty{Name: buyer},
		EvidenceAttachmentID: evidenceID,
	}
	if date, ok := date(facts.IssueDate); ok {
		invoice.InvoiceDate = date
	}
	if tax, ok := amount(facts.Tax); ok {
		invoice.TaxAmount = tax
	}
	if pretax, ok := amount(facts.Pretax); ok {
		invoice.AmountWithoutTax = pretax
	}
	return invoice, true
}

func amount(value string) (json.Number, bool) { return invoice.Decimal(value) }

func currencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func date(value string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006.01.02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("2006-01-02"), true
		}
	}
	return "", false
}
