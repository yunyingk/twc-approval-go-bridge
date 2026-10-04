package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testProfile() BusinessProfile {
	return BusinessProfile{
		Version: 1, Name: "test",
		Tables: BusinessTables{
			Transactions:         TableBinding{Name: "transactions", Source: "third_party_webhook", Access: "read_only", BaseToken: "base", TableID: "transactions", Fields: map[string]string{"transaction_id": "transaction"}},
			ReimbursementDetails: TableBinding{Name: "details", Source: "employee", Access: "read_write", BaseToken: "base", TableID: "details", Fields: map[string]string{"attachment": "attachment", "detail_id": "detail", "transaction_relation": "relation"}},
			InvoiceLedger:        TableBinding{Name: "ledger", Source: "bridge", Access: "read_write", BaseToken: "base", TableID: "ledger", Fields: map[string]string{"source_key": "source", "raw_json": "raw", "invoice_number": "number"}},
		},
		Recognition: RecognitionSettings{Provider: "disabled", TriggerMode: "both"},
		Review:      ReviewSettings{Provider: "seal", TriggerMode: "manual", ContextFields: map[string]string{"reason": "reason"}, ResultFields: map[string]string{"decision": "decision", "document_id": "document", "revision": "revision"}},
	}
}

func writeProfile(t *testing.T, profile BusinessProfile) string {
	t.Helper()
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "business.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfileSwitchIsCompleteAndOverridesStaleEnvironment(t *testing.T) {
	for _, key := range []string{"FEISHU_APP_ID", "FEISHU_APP_SECRET", "SEAL_CALLBACK_TOKEN"} {
		t.Setenv(key, "")
	}
	// Invalid stale values must neither supply bindings nor prevent profile loading.
	for _, key := range []string{"RECEIPT_BASE_TOKEN", "RECEIPT_TABLE_ID", "RECEIPT_ATTACHMENT_FIELD_ID", "RECEIPT_LEDGER_TABLE_ID", "RECEIPT_SOURCE_DETAIL_FIELD_ID", "RECEIPT_PROVIDER", "RECEIPT_TRIGGER_MODE", "REVIEW_PROVIDER", "REVIEW_TRIGGER_MODE", "RECEIPT_LEDGER_FIELD_IDS", "REVIEW_CONTEXT_FIELD_IDS", "REVIEW_RESULT_FIELD_IDS"} {
		t.Setenv(key, "stale-invalid")
	}
	first := testProfile()
	t.Setenv("BUSINESS_CONFIG_FILE", writeProfile(t, first))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReceiptBaseToken != "base" || cfg.ReceiptTableID != "details" || cfg.ReceiptFieldID != "attachment" ||
		cfg.ReceiptLedgerTableID != "ledger" || cfg.ReceiptSourceDetailFieldID != "detail" || cfg.ReceiptProvider != "" ||
		cfg.ReviewTriggerMode != "manual" || !reflect.DeepEqual(cfg.ReviewContextFieldIDs, first.Review.ContextFields) ||
		!reflect.DeepEqual(cfg.ReviewResultFieldIDs, first.Review.ResultFields) || !reflect.DeepEqual(cfg.ReceiptLedgerFieldIDs, first.Tables.InvoiceLedger.Fields) {
		t.Fatal("profile did not replace the complete legacy binding set")
	}
	second := testProfile()
	second.Name = "another-company"
	second.Tables.Transactions.BaseToken = "transaction-base"
	second.Tables.Transactions.TableID = "other-transactions"
	second.Tables.ReimbursementDetails.BaseToken = "new-base"
	second.Tables.ReimbursementDetails.TableID = "new-details"
	second.Tables.ReimbursementDetails.Fields["attachment"] = "new-attachment"
	second.Tables.InvoiceLedger.BaseToken = "new-base"
	second.Tables.InvoiceLedger.TableID = "new-ledger"
	second.Tables.InvoiceLedger.Fields["source_key"] = "new-source"
	second.Review.ResultFields["decision"] = "new-decision"
	t.Setenv("BUSINESS_CONFIG_FILE", writeProfile(t, second))
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Business.Name != second.Name || cfg.ReceiptBaseToken != "new-base" || cfg.ReceiptTableID != "new-details" ||
		cfg.ReceiptFieldID != "new-attachment" || cfg.ReceiptLedgerTableID != "new-ledger" ||
		cfg.ReceiptLedgerFieldIDs["source_key"] != "new-source" || cfg.ReviewResultFieldIDs["decision"] != "new-decision" ||
		cfg.Business.Tables.Transactions.BaseToken != "transaction-base" {
		t.Fatal("switch mixed old and new business bindings")
	}
}

func TestProfileKeepsRuntimeCredentialsAndAppliesAutomaticReview(t *testing.T) {
	profile := testProfile()
	profile.Recognition.Provider = "anyreceipt"
	profile.Review.TriggerMode = "after_recognition"
	t.Setenv("BUSINESS_CONFIG_FILE", writeProfile(t, profile))
	t.Setenv("FEISHU_APP_ID", "app")
	t.Setenv("FEISHU_APP_SECRET", "secret-from-environment")
	t.Setenv("ANYRECEIPT_API_KEY", "key-from-environment")
	t.Setenv("SEAL_CALLBACK_TOKEN", "abcdefghijklmnopqrstuvwxyz012345")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FeishuAppSecret != "secret-from-environment" || cfg.AnyreceiptAPIKey != "key-from-environment" ||
		cfg.ReceiptProvider != "anyreceipt" || cfg.ReviewTriggerMode != "after_recognition" {
		t.Fatal("profile replaced credentials or lost automatic review configuration")
	}
	t.Setenv("SEAL_CALLBACK_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("file-based automatic Seal review bypassed callback requirements")
	}
}

func TestProfileRejectsUnsafeOrUnsupportedBindings(t *testing.T) {
	cases := []struct {
		name   string
		change func(*BusinessProfile)
	}{
		{"version", func(p *BusinessProfile) { p.Version = 2 }},
		{"missing table", func(p *BusinessProfile) { p.Tables.Transactions.TableID = "" }},
		{"missing attachment", func(p *BusinessProfile) { delete(p.Tables.ReimbursementDetails.Fields, "attachment") }},
		{"missing ledger raw evidence", func(p *BusinessProfile) { delete(p.Tables.InvoiceLedger.Fields, "raw_json") }},
		{"duplicate roles", func(p *BusinessProfile) { p.Tables.InvoiceLedger.TableID = "details" }},
		{"cross Base ledger", func(p *BusinessProfile) { p.Tables.InvoiceLedger.BaseToken = "other-base" }},
		{"transaction write", func(p *BusinessProfile) { p.Tables.Transactions.Access = "read_write" }},
		{"duplicate ledger columns", func(p *BusinessProfile) { p.Tables.InvoiceLedger.Fields["raw_json"] = "source" }},
		{"output overwrites relationship", func(p *BusinessProfile) { p.Review.ResultFields["decision"] = "relation" }},
		{"invalid trigger", func(p *BusinessProfile) { p.Review.TriggerMode = "anything" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := testProfile()
			tc.change(&profile)
			if _, err := LoadBusinessProfile(writeProfile(t, profile)); err == nil {
				t.Fatal("unsafe or unsupported profile accepted")
			}
		})
	}
}

func TestProfileRejectsUnknownKeysAndExtraObjects(t *testing.T) {
	raw, err := json.Marshal(testProfile())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		strings.TrimSuffix(string(raw), "}") + `,"app_secret":"must-not-belong-here"}`,
		strings.Replace(string(raw), `"trigger_mode"`, `"triger_mode"`, 1),
		string(raw) + `{}`,
		`{"version":`,
	} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBusinessProfile(path); err == nil {
			t.Fatal("unknown key, malformed JSON or extra object accepted")
		}
	}
}

func TestLinkedTransactionReviewRequiresExplicitCompleteNativeBinding(t *testing.T) {
	profile := testProfile()
	profile.Review.IncludeTransactions = true
	for _, semantic := range []string{"original_amount", "original_currency", "merchant", "transaction_time"} {
		profile.Tables.Transactions.Fields[semantic] = semantic + "-field"
	}
	if _, err := LoadBusinessProfile(writeProfile(t, profile)); err != nil {
		t.Fatal(err)
	}
	profile.Tables.Transactions.BaseToken = "other-base"
	if _, err := LoadBusinessProfile(writeProfile(t, profile)); err == nil {
		t.Fatal("native transaction relation accepted another Base")
	}
	profile.Tables.Transactions.BaseToken = "base"
	delete(profile.Tables.Transactions.Fields, "original_currency")
	if _, err := LoadBusinessProfile(writeProfile(t, profile)); err == nil {
		t.Fatal("payment review accepted an incomplete currency binding")
	}
	profile.Review.IncludeTransactions = false
	profile.Review.ContextFields["bridge_transaction_evidence"] = "input"
	if _, err := LoadBusinessProfile(writeProfile(t, profile)); err == nil {
		t.Fatal("source context can replace provider-generated payment evidence")
	}
}
