// Package sealmapper turns recognized receipts into Seal AI document requests.
// It is deliberately separate from both the Feishu adapter and the generic Seal client.
package sealmapper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/aggregate"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
)

var amountPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

// Input contains the facts available after OCR and a successful Seal upload.
// StartTime is supplied by the caller so retries and tests can control it.
type Input struct {
	DocumentID  string
	DocumentSN  string
	RecordID    string
	FileToken   string
	FileName    string
	Recognition receipt.Recognition
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
	addText("seal_attachment_id", "Seal 附件 ID", input.Upload.AttachmentID)
	if info := input.Upload.Attachment; completeAttachmentInfo(info) {
		document.Fields = append(document.Fields, seal.DocumentField{
			Key: "receipt_attachment", Label: "发票附件", Type: "ATTACHMENT", Value: []seal.AttachmentInfo{info},
		})
	}
	addText("receipt_title", "发票摘要", text(input.Recognition, "title", input.Recognition.Title))
	addText("receipt_number", "发票号", text(input.Recognition, "Number", input.Recognition.Number))
	addText("receipt_type", "票据类型", text(input.Recognition, "type", input.Recognition.Type))
	addText("receipt_business_category", "业务分类", text(input.Recognition, "TypeofBill", ""))
	addText("receipt_seller", "开票方", text(input.Recognition, "Seller", ""))
	addText("receipt_buyer", "购买方", text(input.Recognition, "Buyer", ""))
	addText("receipt_currency", "币种", text(input.Recognition, "currency", ""))
	addText("receipt_total", "含税金额", text(input.Recognition, "total", input.Recognition.Total))
	addText("receipt_tax", "税额", text(input.Recognition, "tax", input.Recognition.Tax))
	addText("receipt_date", "开票日期", text(input.Recognition, "DateofInssuance", input.Recognition.Date))
	addText("receipt_country", "国家", text(input.Recognition, "country", input.Recognition.Country))
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
		result.Fields = append(result.Fields, seal.DocumentField{
			Key: fmt.Sprintf("duplicate_candidate_%02d", index+1), Label: "台账查重候选", Type: "TEXT",
			Value: fmt.Sprintf("当前来源 %s；候选台账记录 %s；候选来源 %s；标记 %s",
				finding.CurrentSourceKey, finding.PriorRecordID, finding.PriorSourceKey, finding.Code),
		})
	}
	return result, nil
}

func mapInvoice(input Input, sourceID string) (seal.ExternalInvoice, bool) {
	number := text(input.Recognition, "Number", input.Recognition.Number)
	currency := strings.ToUpper(text(input.Recognition, "currency", ""))
	total, totalOK := amount(text(input.Recognition, "total", input.Recognition.Total))
	seller := text(input.Recognition, "Seller", "")
	buyer := text(input.Recognition, "Buyer", "")
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
	if date, ok := date(text(input.Recognition, "DateofInssuance", input.Recognition.Date)); ok {
		invoice.InvoiceDate = date
	}
	if tax, ok := amount(text(input.Recognition, "tax", input.Recognition.Tax)); ok {
		invoice.TaxAmount = tax
	}
	if pretax, ok := amount(text(input.Recognition, "amountwithouttax", "")); ok {
		invoice.AmountWithoutTax = pretax
	}
	return invoice, true
}

func text(recognition receipt.Recognition, key, fallback string) string {
	if raw := recognition.Outputs[key]; len(raw) != 0 {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			return number.String()
		}
	}
	return strings.TrimSpace(fallback)
}

func amount(value string) (json.Number, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	if !amountPattern.MatchString(value) || !json.Valid([]byte(value)) {
		return "", false
	}
	return json.Number(value), true
}

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

func completeAttachmentInfo(info seal.AttachmentInfo) bool {
	return strings.TrimSpace(info.Name) != "" &&
		strings.TrimSpace(info.MimeType) != "" &&
		strings.TrimSpace(info.URL) != "" &&
		strings.TrimSpace(info.OSSPath) != "" &&
		strings.TrimSpace(info.OSSSignedURL) != "" &&
		info.OSSFileSize > 0
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
