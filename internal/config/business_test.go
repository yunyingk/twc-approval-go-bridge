package config

import (
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestReviewChangeConfigurationRequiresAutomaticTriggerAndBoundedDebounce(t *testing.T) {
	p := testProfile()
	if p.Review.ResubmitOnDetailChange {
		t.Fatal("changes should default off")
	}
	p.Review.ResubmitOnDetailChange = true
	if err := p.validate(); err == nil {
		t.Fatal("manual mode enabled background change submissions")
	}
	p.Review.TriggerMode = "after_recognition"
	for _, value := range []string{"", "1s", "10s", "10m"} {
		p.Review.ChangeDebounce = value
		if err := p.validate(); err != nil {
			t.Fatalf("valid debounce %s rejected: %v", value, err)
		}
	}
	for _, value := range []string{"zero", "0s", "-1s", "500ms", (11 * time.Minute).String()} {
		p.Review.ChangeDebounce = value
		if err := p.validate(); err == nil {
			t.Fatalf("invalid debounce accepted: %s", value)
		}
	}
}

func TestSourceChangeConfigurationIsIndependentAndRequiresInvoiceNumber(t *testing.T) {
	p := testProfile()
	p.Review.ResubmitOnSourceChange = true
	if err := p.validate(); err == nil {
		t.Fatal("manual mode enabled source-change submissions")
	}
	p.Review.TriggerMode = "after_recognition"
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	if p.Review.ResubmitOnDetailChange {
		t.Fatal("source change option forced detail edits on")
	}
	delete(p.Tables.InvoiceLedger.Fields, "invoice_number")
	if err := p.validate(); err == nil {
		t.Fatal("source dependency lookup has no invoice number binding")
	}
}

func writeProfile(t *testing.T, profile BusinessProfile) string {
	t.Helper()
	d := Document{
		BusinessProfile: profile,
		Recognition:     profile.Recognition,
		Review:          profile.Review,
	}
	return writeDocument(t, d)
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
	t.Setenv("CONFIG_FILE", writeProfile(t, first))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	detail := cfg.Business.Tables.ReimbursementDetails
	ledger := cfg.Business.Tables.InvoiceLedger
	if detail.BaseToken != "base" || detail.TableID != "details" || detail.Fields["attachment"] != "attachment" ||
		ledger.TableID != "ledger" || detail.Fields["detail_id"] != "detail" || cfg.ReceiptProvider() != "" ||
		cfg.ReviewTriggerMode() != "manual" || !reflect.DeepEqual(cfg.Business.Review.ContextFields, first.Review.ContextFields) ||
		!reflect.DeepEqual(cfg.Business.Review.ResultFields, first.Review.ResultFields) || !reflect.DeepEqual(ledger.Fields, first.Tables.InvoiceLedger.Fields) {
		t.Fatal("selected file did not supply the complete binding set")
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
	t.Setenv("CONFIG_FILE", writeProfile(t, second))
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	detail2 := cfg.Business.Tables.ReimbursementDetails
	ledger2 := cfg.Business.Tables.InvoiceLedger
	if cfg.Business.Name != second.Name || detail2.BaseToken != "new-base" || detail2.TableID != "new-details" ||
		detail2.Fields["attachment"] != "new-attachment" || ledger2.TableID != "new-ledger" ||
		ledger2.Fields["source_key"] != "new-source" || cfg.Business.Review.ResultFields["decision"] != "new-decision" ||
		cfg.Business.Tables.Transactions.BaseToken != "transaction-base" {
		t.Fatal("switch mixed old and new business bindings")
	}
}

func TestSingleDocumentCredentialsAndAutomaticReview(t *testing.T) {
	d := Document{BusinessProfile: testProfile(), Feishu: FeishuSettings{AppCredentials: AppCredentials{AppID: "app", AppSecret: "file-secret"}}, Anyreceipt: AnyreceiptSettings{APIKey: "file-key"}, Seal: SealSettings{CallbackToken: "abcdefghijklmnopqrstuvwxyz012345"}}
	d.Recognition.Provider = "anyreceipt"
	d.Review.TriggerMode = "after_recognition"
	cfg, err := loadDocument(t, d)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Feishu.AppSecret != "file-secret" || cfg.Anyreceipt.APIKey != "file-key" || cfg.ReceiptProvider() != "anyreceipt" || cfg.ReviewTriggerMode() != "after_recognition" {
		t.Fatal("complete document did not supply automatic review")
	}
	d.Seal.CallbackToken = ""
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("automatic Seal review bypassed callback requirement")
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
			if _, err := LoadFile(writeProfile(t, profile)); err == nil {
				t.Fatal("unsafe or unsupported profile accepted")
			}
		})
	}
}

func TestProfileRejectsUnknownKeysAndInvalidTOML(t *testing.T) {
	raw, err := toml.Marshal(testProfile())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"app_secret = \"must-not-belong-here\"\n" + string(raw),
		strings.Replace(string(raw), "trigger_mode", "triger_mode", 1),
		string(raw) + "\n[recognition]\nprovider = \"disabled\"\n",
		`version =`,
	} {
		path := filepath.Join(t.TempDir(), "invalid.toml")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil {
			t.Fatal("unknown key, malformed TOML or duplicate section accepted")
		}
	}
}

func TestLinkedTransactionReviewRequiresExplicitCompleteNativeBinding(t *testing.T) {
	profile := testProfile()
	profile.Review.IncludeTransactions = true
	for _, semantic := range []string{"original_amount", "original_currency", "merchant", "transaction_time"} {
		profile.Tables.Transactions.Fields[semantic] = semantic + "-field"
	}
	if _, err := LoadFile(writeProfile(t, profile)); err != nil {
		t.Fatal(err)
	}
	profile.Tables.Transactions.BaseToken = "other-base"
	if _, err := LoadFile(writeProfile(t, profile)); err == nil {
		t.Fatal("native transaction relation accepted another Base")
	}
	profile.Tables.Transactions.BaseToken = "base"
	delete(profile.Tables.Transactions.Fields, "original_currency")
	if _, err := LoadFile(writeProfile(t, profile)); err == nil {
		t.Fatal("payment review accepted an incomplete currency binding")
	}
	profile.Review.IncludeTransactions = false
	profile.Review.ContextFields["bridge_transaction_evidence"] = "input"
	if _, err := LoadFile(writeProfile(t, profile)); err == nil {
		t.Fatal("source context can replace provider-generated payment evidence")
	}
}
