package mapper

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
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
	inv := document.Invoices[0]
	if inv.SourceInvoiceID != "feishu-invoice-rec123" || inv.Kind != "invoice" || inv.InvoiceType != "overseas" ||
		inv.CurrencyCode != "USD" || inv.TotalAmount.String() != "1234.50" ||
		inv.TaxAmount.String() != "34.50" || inv.AmountWithoutTax.String() != "1200" ||
		inv.InvoiceDate != "2026-09-29" || inv.EvidenceAttachmentID != "seal-attachment-1" {
		t.Fatalf("unexpected invoice: %#v", inv)
	}
	if inv.ClaimedAmount.String() != "1234.50" || len(inv.ExpenseRefs) != 1 ||
		inv.ExpenseRefs[0].SourceExpenseID != "rec123" || inv.ExpenseRefs[0].AllocatedAmount.String() != "1234.50" ||
		inv.ExpenseRefs[0].CurrencyCode != "USD" {
		t.Fatalf("unexpected claimedAmount or expenseRefs: %#v", inv)
	}

	expDetails, ok := findField(document.Fields, "expense_details")
	if !ok || expDetails.SemanticType != "EXPENSE_DETAIL_LIST" {
		t.Fatalf("expected expense_details LIST with EXPENSE_DETAIL_LIST: %#v", document.Fields)
	}
	rows, ok := expDetails.Value.([][]seal.DocumentField)
	if !ok || len(rows) != 1 {
		t.Fatalf("expected 1 row in expense_details: %#v", expDetails.Value)
	}
	if !hasField(rows[0], "source_invoice_attachment_lookup", "ATTACHMENT") {
		t.Fatalf("expected source_invoice_attachment_lookup in row: %#v", rows[0])
	}
	if !hasField(rows[0], "detailId", "TEXT") || !hasField(rows[0], "item_amount", "AMOUNT") {
		t.Fatalf("expected detailId and item_amount in row: %#v", rows[0])
	}

	again, err := Map(input)
	if err != nil || again.DocumentID != document.DocumentID || again.DocumentSN != document.DocumentSN ||
		again.Invoices[0].SourceInvoiceID != inv.SourceInvoiceID {
		t.Fatalf("source identity should be stable across retries: %#v, %v", again, err)
	}
	input.FileToken = "another-file"
	input.RecordID = "another-record"
	other, err := Map(input)
	if err != nil || other.DocumentID != document.DocumentID || other.DocumentSN != document.DocumentSN ||
		other.Invoices[0].SourceInvoiceID == inv.SourceInvoiceID {
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
			Attachment: seal.AttachmentInfo{
				Name: "receipt.jpg", MimeType: "image/jpeg", URL: "https://example.com/file",
				OSSPath: "path", OSSSignedURL: "https://example.com/signed", OSSFileSize: 50,
			},
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
	expDetails, ok := findField(document.Fields, "expense_details")
	if !ok || expDetails.SemanticType != "EXPENSE_DETAIL_LIST" {
		t.Fatalf("expected expense_details LIST: %#v", document.Fields)
	}
	rows, ok := expDetails.Value.([][]seal.DocumentField)
	if !ok || len(rows) != 1 {
		t.Fatalf("expected 1 row in expense_details: %#v", expDetails.Value)
	}
	if !hasField(rows[0], "source_invoice_attachment_lookup", "ATTACHMENT") {
		t.Fatal("the original must be an attachment field inside expense_details row")
	}
}

func TestMapRejectsTextOnlyUpload(t *testing.T) {
	_, err := Map(Input{
		DocumentID: "doc", DocumentSN: "sn", RecordID: "record", FileToken: "file",
		StartTime: time.Unix(100, 0), Upload: seal.UploadResponse{AttachmentID: "id"},
	})
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

func TestBatchProvidesDuplicateFactsAndSearchLimits(t *testing.T) {
	prior := dupcheck.Invoice{
		RecordID: "old", SourceKey: "old:file", Number: "INV-1", Seller: "Shop", Type: "invoice",
		IssueDate: "2026-09-29", Total: "9007199254740993.01", Currency: "USD",
	}
	current := dupcheck.Invoice{SourceKey: "rec:file", Number: "INV-1", Seller: "Shop", Type: "invoice"}
	batch := aggregate.Document{
		DocumentID: "doc", DocumentSN: "sn", RecordID: "rec", StartTime: time.Unix(100, 0),
		Invoices: []aggregate.Invoice{{FileToken: "file", Facts: current, Candidates: []dupcheck.Invoice{prior}}},
		Findings: dupcheck.Compare(current, []dupcheck.Invoice{prior}),
	}
	uploads := map[string]seal.UploadResponse{
		"file": {
			AttachmentID: "id",
			Attachment: seal.AttachmentInfo{
				Name: "receipt.jpg", MimeType: "image/jpeg", URL: "https://example.com/file",
				OSSPath: "path", OSSSignedURL: "https://example.com/signed", OSSFileSize: 50,
			},
		},
	}
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
	if doc.DocumentURL != recordURL || doc.SourceDocumentURL != recordURL {
		t.Fatalf("expected DocumentURL and SourceDocumentURL %q, got %q, %q", recordURL, doc.DocumentURL, doc.SourceDocumentURL)
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
	if docWithout.DocumentURL != "" || docWithout.SourceDocumentURL != "" {
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
	if batchRes.DocumentURL != recordURL || batchRes.SourceDocumentURL != recordURL {
		t.Fatalf("expected batch DocumentURL and SourceDocumentURL %q", recordURL)
	}
	topFound := false
	for _, f := range batchRes.Fields {
		if f.Key == "source_record_url" && f.Value == recordURL {
			topFound = true
		}
	}
	if !topFound {
		t.Fatalf("expected top-level source_record_url field in batch")
	}
}

func TestBuildDocumentWithFullContextAndTransactions(t *testing.T) {
	batch := aggregate.Document{
		DocumentID: "feishu-expense-rec100",
		DocumentSN: "EXP-DEMO-017",
		RecordID:   "rec100",
		StartTime:  time.Unix(1791561600, 0),
		Invoices: []aggregate.Invoice{
			{
				FileToken:      "tok-1",
				LedgerRecordID: "reczz1",
				Recognition: invoice.Recognition{
					Summary: "service fee",
					Outputs: map[string]json.RawMessage{
						"Number":   json.RawMessage(`"F896D966-0001"`),
						"currency": json.RawMessage(`"USD"`),
						"total":    json.RawMessage(`"20"`),
						"Seller":   json.RawMessage(`"OpenAI, LLC"`),
						"Buyer":    json.RawMessage(`"Teacher Tools"`),
					},
				},
				Facts: dupcheck.Invoice{
					RecordID:  "reczz1",
					SourceKey: "rec100:tok-1",
					Number:    "F896D966-0001",
					Seller:    "OpenAI, LLC",
					Currency:  "USD",
					Total:     "20",
				},
			},
		},
	}
	uploads := map[string]seal.UploadResponse{
		"tok-1": {
			AttachmentID: "att-openai-1",
			Attachment: seal.AttachmentInfo{
				Name: "invoice-1.jpg", MimeType: "image/jpeg", URL: "https://seal.ai/1",
				OSSPath: "p1", OSSSignedURL: "https://seal.ai/signed/1", OSSFileSize: 1000,
			},
		},
	}
	ctx := map[string]string{
		"reimbursement_submitter_name":      "朱益涛",
		"reimbursement_submitter_source_id": "ou_test123",
		"expense_category":                  "市场推广",
		"purpose":                           "测试 消费",
	}
	tx := &core.TransactionEvidence{
		Source: "feishu:base:tx",
		Transactions: []core.Transaction{
			{
				RecordID:           "rec-tx-1",
				TransactionID:      "111",
				Merchant:           "测试",
				OccurredAt:         "2026-10-09",
				OriginalAmount:     "100",
				OriginalCurrency:   "USA",
				BookedAmountCNY:    "660",
				SettlementCurrency: "CNY",
				CardholderName:     "朱益涛",
				CardLastFour:       "1261",
				Country:            "美国",
				TransactionType:    "消费",
				PaymentChannel:     "万里汇虚拟卡",
				Status:             "成功",
			},
		},
	}

	doc, err := BuildDocument(DocumentParams{
		Batch:        batch,
		Uploads:      uploads,
		Context:      ctx,
		Transactions: tx,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify expense_details
	ed, ok := findField(doc.Fields, "expense_details")
	if !ok || ed.SemanticType != "EXPENSE_DETAIL_LIST" {
		t.Fatalf("expected expense_details with EXPENSE_DETAIL_LIST, got %#v", ed)
	}
	rows := ed.Value.([][]seal.DocumentField)
	if len(rows) != 1 || len(rows[0]) != 25 {
		t.Fatalf("expected 1 row with 25 fields, got row count %d with %d fields", len(rows), len(rows[0]))
	}

	// Verify single-value fields
	checkField := func(key, expectedType string, checkVal func(any) bool) {
		f, ok := findField(doc.Fields, key)
		if !ok {
			t.Fatalf("field %q missing from document", key)
		}
		if f.Type != expectedType {
			t.Fatalf("field %q expected type %s, got %s", key, expectedType, f.Type)
		}
		if checkVal != nil && !checkVal(f.Value) {
			t.Fatalf("field %q value check failed: %#v", key, f.Value)
		}
	}

	checkField("reimbursement_id", "TEXT", func(v any) bool { return v == "EXP-DEMO-017" })
	checkField("reimbursement_submitter_name", "TEXT", func(v any) bool { return v == "朱益涛" })
	checkField("reimbursement_submitter_source_id", "TEXT", func(v any) bool { return v == "ou_test123" })
	checkField("expense_category", "TEXT", func(v any) bool { return v == "市场推广" })
	checkField("transaction_amount", "AMOUNT", func(v any) bool {
		av, ok := v.(seal.AmountValue)
		return ok && av.Currency == "USA" && av.Amount.(json.Number).String() == "100"
	})
	checkField("transaction_time", "DATE", func(v any) bool { return v == "2026-10-09" })
	checkField("expense_date", "DATE", func(v any) bool { return v == "2026-10-09" })
	checkField("purpose", "TEXT", func(v any) bool { return v == "测试 消费" })
	checkField("transaction_currency", "TEXT", func(v any) bool { return v == "USA" })
	checkField("transaction_id", "TEXT", func(v any) bool { return v == "111" })
	checkField("merchant", "TEXT", func(v any) bool { return v == "测试" })
	checkField("cardholder_name", "TEXT", func(v any) bool { return v == "朱益涛" })
	checkField("card_last_four", "TEXT", func(v any) bool { return v == "1261" })
	checkField("settlement_amount", "AMOUNT", func(v any) bool {
		av, ok := v.(seal.AmountValue)
		return ok && av.Currency == "CNY" && av.Amount.(json.Number).String() == "660"
	})
	checkField("merchant_region", "TEXT", func(v any) bool { return v == "美国" })
	checkField("payment_channel", "TEXT", func(v any) bool { return v == "万里汇虚拟卡" })
	checkField("exchange_rate", "NUMBER", func(v any) bool { return v == "" })
	checkField("transaction_status", "TEXT", func(v any) bool { return v == "成功" })
	checkField("project_number", "TEXT", func(v any) bool { return v == "" })
	checkField("project_record_id", "TEXT", func(v any) bool { return v == "" })

	// Verify structured invoice
	if len(doc.Invoices) != 1 {
		t.Fatalf("expected 1 invoice, got %d", len(doc.Invoices))
	}
	inv := doc.Invoices[0]
	if inv.SourceInvoiceID != "feishu-invoice-reczz1" {
		t.Fatalf("expected SourceInvoiceID feishu-invoice-reczz1, got %s", inv.SourceInvoiceID)
	}
	if inv.ClaimedAmount.String() != "20" || len(inv.ExpenseRefs) != 1 ||
		inv.ExpenseRefs[0].SourceExpenseID != "reczz1" || inv.ExpenseRefs[0].AllocatedAmount.String() != "20" ||
		inv.ExpenseRefs[0].CurrencyCode != "USD" {
		t.Fatalf("unexpected claimedAmount or expenseRefs: %#v", inv)
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

func findField(fields []seal.DocumentField, key string) (seal.DocumentField, bool) {
	for _, field := range fields {
		if field.Key == key {
			return field, true
		}
	}
	return seal.DocumentField{}, false
}
