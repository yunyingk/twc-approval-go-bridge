package mapper

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
)

func TestMapCompleteReceipt(t *testing.T) {
	input := Input{
		DocumentID: "reimbursement-rec123",
		DocumentSN: "REIMB-123",
		RecordID:   "rec123",
		FileToken:  "file456",
		FileName:   "receipt.jpg",
		StartTime:  time.Unix(1_800_000_000, 0),
		Upload: seal.UploadResponse{
			AttachmentID: "seal-attachment-1",
			Attachment: seal.AttachmentInfo{
				Name: "receipt.jpg", MimeType: "image/jpeg", URL: "https://example.com/file",
				OSSPath: "path", OSSSignedURL: "https://example.com/signed", OSSFileSize: 50,
			},
		},
		Recognition: invoice.Recognition{
			Summary: "team dinner",
			Outputs: map[string]json.RawMessage{
				"Number":           json.RawMessage(`"INV-9"`),
				"currency":         json.RawMessage(`"usd"`),
				"total":            json.RawMessage(`"1,234.50"`),
				"tax":              json.RawMessage(`"34.50"`),
				"amountwithouttax": json.RawMessage(`"1,200"`),
				"Seller":           json.RawMessage(`"Merchant Inc"`),
				"Buyer":            json.RawMessage(`"Employee"`),
				"DateofInssuance":  json.RawMessage(`"2026/09/29"`),
			},
		},
	}
	document, err := Map(input)
	if err != nil {
		t.Fatal(err)
	}
	if document.DocumentID == "" || document.DocumentSN == "" || document.StartTime != input.StartTime.Unix() {
		t.Fatalf("unexpected document identity: %#v", document)
	}
	if len(document.Invoices) != 1 {
		t.Fatalf("expected one structured invoice: %#v", document.Invoices)
	}
	invoice := document.Invoices[0]
	if invoice.SourceInvoiceID != "rec123:file456" || invoice.Kind != "invoice" || invoice.InvoiceType != "overseas" ||
		invoice.CurrencyCode != "USD" || invoice.TotalAmount.String() != "1234.50" ||
		invoice.TaxAmount.String() != "34.50" || invoice.AmountWithoutTax.String() != "1200" ||
		invoice.InvoiceDate != "2026-09-29" || invoice.EvidenceAttachmentID != "seal-attachment-1" {
		t.Fatalf("unexpected invoice: %#v", invoice)
	}
	if !hasField(document.Fields, "receipt_attachment", "ATTACHMENT") {
		t.Fatalf("expected original attachment field: %#v", document.Fields)
	}
	again, err := Map(input)
	if err != nil || again.DocumentID != document.DocumentID || again.DocumentSN != document.DocumentSN ||
		again.Invoices[0].SourceInvoiceID != invoice.SourceInvoiceID {
		t.Fatalf("source identity should be stable across retries: %#v, %v", again, err)
	}
	input.FileToken = "another-file"
	other, err := Map(input)
	if err != nil || other.DocumentID != document.DocumentID || other.DocumentSN != document.DocumentSN ||
		other.Invoices[0].SourceInvoiceID == invoice.SourceInvoiceID {
		t.Fatalf("same reimbursement and different attachment identities expected: %#v, %v", other, err)
	}
}

func TestMapIncompleteReceiptStillMakesDocument(t *testing.T) {
	document, err := Map(Input{
		DocumentID: "reimbursement-rec123",
		DocumentSN: "REIMB-123",
		RecordID:   "rec123",
		FileToken:  "file456",
		StartTime:  time.Unix(1_800_000_000, 0),
		Upload: seal.UploadResponse{
			AttachmentID: "seal-attachment-1",
			Attachment: seal.AttachmentInfo{Name: "receipt.jpg", MimeType: "image/jpeg", URL: "https://example.com/file",
				OSSPath: "path", OSSSignedURL: "https://example.com/signed", OSSFileSize: 50},
		},
		Recognition: invoice.Recognition{
			Number: "INV-9", Total: "100",
			Outputs: map[string]json.RawMessage{
				"Seller": json.RawMessage(`"Merchant"`),
				"Buyer":  json.RawMessage(`"Employee"`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Invoices) != 0 {
		t.Fatalf("currency is missing, so structured invoice must be omitted: %#v", document.Invoices)
	}
	if !hasField(document.Fields, "receipt_attachment", "ATTACHMENT") || hasField(document.Fields, "seal_attachment_id", "TEXT") {
		t.Fatal("the original must be an attachment field, not a text ID")
	}
}

func TestMapRejectsTextOnlyUpload(t *testing.T) {
	_, err := Map(Input{DocumentID: "doc", DocumentSN: "sn", RecordID: "record", FileToken: "file",
		StartTime: time.Unix(100, 0), Upload: seal.UploadResponse{AttachmentID: "id"}})
	if err == nil {
		t.Fatal("missing attachment metadata must stop Seal submission")
	}
}

func TestMapRejectsMissingSourceAndStartTime(t *testing.T) {
	if _, err := Map(Input{RecordID: "record", FileToken: "file", StartTime: time.Now()}); err == nil {
		t.Fatal("missing document identity should fail")
	}
	if _, err := Map(Input{DocumentID: "doc", DocumentSN: "sn", FileToken: "file", StartTime: time.Now()}); err == nil {
		t.Fatal("missing record ID should fail")
	}
	if _, err := Map(Input{DocumentID: "doc", DocumentSN: "sn", RecordID: "record", FileToken: "file"}); err == nil {
		t.Fatal("missing upload ID should fail")
	}
	if _, err := Map(Input{DocumentID: "doc", DocumentSN: "sn", RecordID: "record", FileToken: "file", Upload: seal.UploadResponse{AttachmentID: "uploaded"}}); err == nil {
		t.Fatal("missing start time should fail")
	}
}

func hasField(fields []seal.DocumentField, key, fieldType string) bool {
	for _, field := range fields {
		if field.Key == key && field.Type == fieldType {
			return true
		}
	}
	return false
}

func TestBatchProvidesDuplicateFactsAndSearchLimits(t *testing.T) {
	prior := dupcheck.Invoice{RecordID: "old", SourceKey: "old:file", Number: "INV-1", Seller: "Shop", Type: "invoice", IssueDate: "2026-09-29", Total: "9007199254740993.01", Currency: "USD"}
	current := dupcheck.Invoice{SourceKey: "rec:file", Number: "INV-1", Seller: "Shop", Type: "invoice"}
	batch := aggregate.Document{DocumentID: "doc", DocumentSN: "sn", RecordID: "rec", StartTime: time.Unix(100, 0),
		Invoices: []aggregate.Invoice{{FileToken: "file", Facts: current, Candidates: []dupcheck.Invoice{prior}}},
		Findings: dupcheck.Compare(current, []dupcheck.Invoice{prior})}
	uploads := map[string]seal.UploadResponse{"file": {AttachmentID: "id", Attachment: seal.AttachmentInfo{Name: "receipt.jpg", MimeType: "image/jpeg", URL: "https://example.com/file", OSSPath: "path", OSSSignedURL: "https://example.com/signed", OSSFileSize: 50}}}
	result, err := MapBatch(batch, uploads)
	if err != nil {
		t.Fatal(err)
	}
	var evidence map[string]string
	for _, field := range result.Fields {
		if field.Key == "duplicate_candidate_01" {
			if err := json.Unmarshal([]byte(field.Value.(string)), &evidence); err != nil {
				t.Fatal(err)
			}
		}
	}
	if evidence["prior_record_id"] != "old" || evidence["seller"] != "Shop" || evidence["issue_date"] != "2026-09-29" || evidence["total_amount"] != prior.Total || evidence["currency"] != "USD" || evidence["code"] != "same_seller_number_type" {
		t.Fatalf("Seal received incomplete candidate evidence: %#v", evidence)
	}
	if !hasField(result.Fields, "duplicate_evidence_scope", "TEXT") {
		t.Fatal("missing search scope")
	}
	batch.Findings = nil
	result, err = MapBatch(batch, uploads)
	if err != nil || !hasField(result.Fields, "duplicate_evidence_scope", "TEXT") {
		t.Fatal("no candidates must still disclose search limits")
	}
}

func TestMapAndBatchIncludesRecordURL(t *testing.T) {
	recordURL := "https://zyt-test.feishu.cn/base/bascn123?table=tbl456&record=rec789"
	input := Input{
		DocumentID: "doc-1",
		DocumentSN: "SN-1",
		RecordID:   "rec789",
		RecordURL:  recordURL,
		FileToken:  "token1",
		FileName:   "test.png",
		StartTime:  time.Unix(100, 0),
		Upload: seal.UploadResponse{
			AttachmentID: "att-1",
			Attachment: seal.AttachmentInfo{
				Name: "test.png", MimeType: "image/png", URL: "https://example.com/test.png",
				OSSPath: "oss", OSSSignedURL: "https://example.com/signed", OSSFileSize: 10,
			},
		},
	}
	doc, err := Map(input)
	if err != nil {
		t.Fatal(err)
	}
	if doc.DocumentURL != recordURL {
		t.Fatalf("expected DocumentURL %q, got %q", recordURL, doc.DocumentURL)
	}
	found := false
	for _, f := range doc.Fields {
		if f.Key == "source_record_url" && f.Value == recordURL {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected source_record_url field with value %q", recordURL)
	}

	// Without record URL
	inputWithoutURL := input
	inputWithoutURL.RecordURL = ""
	docWithout, err := Map(inputWithoutURL)
	if err != nil {
		t.Fatal(err)
	}
	if docWithout.DocumentURL != "" {
		t.Fatalf("expected empty DocumentURL, got %q", docWithout.DocumentURL)
	}
	for _, f := range docWithout.Fields {
		if f.Key == "source_record_url" {
			t.Fatalf("did not expect source_record_url field when RecordURL is empty")
		}
	}

	// Batch mapping
	batch := aggregate.Document{
		DocumentID: "doc-1",
		DocumentSN: "SN-1",
		RecordID:   "rec789",
		RecordURL:  recordURL,
		StartTime:  time.Unix(100, 0),
		Invoices: []aggregate.Invoice{
			{FileToken: "token1", Facts: dupcheck.Invoice{SourceKey: "rec789:token1"}},
		},
	}
	uploads := map[string]seal.UploadResponse{
		"token1": input.Upload,
	}
	batchRes, err := MapBatch(batch, uploads)
	if err != nil {
		t.Fatal(err)
	}
	if batchRes.DocumentURL != recordURL {
		t.Fatalf("expected batch DocumentURL %q, got %q", recordURL, batchRes.DocumentURL)
	}
	topFound := false
	subFound := false
	for _, f := range batchRes.Fields {
		if f.Key == "source_record_url" && f.Value == recordURL {
			topFound = true
		}
		if f.Key == "invoice_01_source_record_url" && f.Value == recordURL {
			subFound = true
		}
	}
	if !topFound {
		t.Fatalf("expected top-level source_record_url field in batch")
	}
	if !subFound {
		t.Fatalf("expected invoice_01_source_record_url field in batch")
	}
}

