package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func writeDocument(t *testing.T, document Document) string {
	t.Helper()
	dir := t.TempDir()
	if document.Recognition.Provider == "" {
		document.Recognition.Provider = document.BusinessProfile.Recognition.Provider
	}
	if document.Recognition.TriggerMode == "" {
		document.Recognition.TriggerMode = document.BusinessProfile.Recognition.TriggerMode
	}
	if document.Recognition.PollInterval == "" {
		document.Recognition.PollInterval = document.BusinessProfile.Recognition.PollInterval
	}
	if document.Recognition.PollStartup == "" {
		document.Recognition.PollStartup = document.BusinessProfile.Recognition.PollStartup
	}
	if document.Review.Provider == "" {
		document.Review.Provider = document.BusinessProfile.Review.Provider
	}
	if document.Review.TriggerMode == "" {
		document.Review.TriggerMode = document.BusinessProfile.Review.TriggerMode
	}
	if document.Review.RulesFile == "" {
		document.Review.RulesFile = document.BusinessProfile.Review.RulesFile
	}
	if !document.Review.IncludeTransactions {
		document.Review.IncludeTransactions = document.BusinessProfile.Review.IncludeTransactions
	}
	if !document.Review.ResubmitOnDetailChange {
		document.Review.ResubmitOnDetailChange = document.BusinessProfile.Review.ResubmitOnDetailChange
	}
	if !document.Review.ResubmitOnSourceChange {
		document.Review.ResubmitOnSourceChange = document.BusinessProfile.Review.ResubmitOnSourceChange
	}
	if document.Review.ChangeDebounce == "" {
		document.Review.ChangeDebounce = document.BusinessProfile.Review.ChangeDebounce
	}
	if document.TablesFile == "" {
		contextFields := document.Review.ContextFields
		if len(contextFields) == 0 {
			contextFields = document.BusinessProfile.Review.ContextFields
		}
		resultFields := document.Review.ResultFields
		if len(resultFields) == 0 {
			resultFields = document.BusinessProfile.Review.ResultFields
		}
		tablesProfile := TablesProfile{
			Version: document.BusinessProfile.Version,
			Name:    document.BusinessProfile.Name,
			Tables: TablesSchema{
				Transactions: document.BusinessProfile.Tables.Transactions,
				ReimbursementDetails: ReimbursementDetailsBinding{
					Name:          document.BusinessProfile.Tables.ReimbursementDetails.Name,
					Source:        document.BusinessProfile.Tables.ReimbursementDetails.Source,
					Access:        document.BusinessProfile.Tables.ReimbursementDetails.Access,
					BaseToken:     document.BusinessProfile.Tables.ReimbursementDetails.BaseToken,
					TableID:       document.BusinessProfile.Tables.ReimbursementDetails.TableID,
					Fields:        document.BusinessProfile.Tables.ReimbursementDetails.Fields,
					ContextFields: contextFields,
					ResultFields:  resultFields,
				},
				InvoiceLedger: document.BusinessProfile.Tables.InvoiceLedger,
			},
		}
		if tablesProfile.Version == 0 {
			tablesProfile.Version = 1
		}
		if tablesProfile.Name == "" {
			tablesProfile.Name = "test"
		}
		tablesRaw, err := json.Marshal(tablesProfile)
		if err != nil {
			t.Fatal(err)
		}
		tablesPath := filepath.Join(dir, "tables.json")
		if err := os.WriteFile(tablesPath, tablesRaw, 0600); err != nil {
			t.Fatal(err)
		}
		document.TablesFile = "tables.json"
	}
	raw, err := toml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadDocument(t *testing.T, document Document) (Config, error) {
	t.Helper()
	t.Setenv("CONFIG_FILE", writeDocument(t, document))
	return Load()
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := loadDocument(t, Document{BusinessProfile: testProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 10*time.Second || cfg.FeishuEnabled() {
		t.Fatal("unexpected runtime defaults")
	}
	if cfg.FeishuEventType != "drive.file.bitable_record_changed_v1" || cfg.ReceiptTriggerMode != "both" || cfg.ReceiptPollStartup != "baseline" || cfg.ReceiptPollInterval != 5*time.Minute || cfg.ReviewTriggerMode != "manual" {
		t.Fatal("unexpected event or trigger defaults")
	}
}

func TestAutomaticReviewRequiresRecognitionDeliveryAndResults(t *testing.T) {
	d := Document{BusinessProfile: testProfile(), Feishu: FeishuSettings{AppCredentials: AppCredentials{AppID: "app", AppSecret: "secret"}}, Anyreceipt: AnyreceiptSettings{APIKey: "key"}}
	d.Review.TriggerMode = "after_recognition"
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("automatic review accepted disabled recognition")
	}
	d.Recognition.Provider = "anyreceipt"
	d.Review.ResultFields = nil
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("automatic review accepted missing result columns")
	}
	d.Review.ResultFields = testProfile().Review.ResultFields
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("automatic Seal review accepted missing callback")
	}
	d.Seal.CallbackToken = "abcdefghijklmnopqrstuvwxyz012345"
	if _, err := loadDocument(t, d); err != nil {
		t.Fatal(err)
	}
	d.Review.TriggerMode = "unknown"
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("unknown trigger accepted")
	}
}

func TestLoadRejectsInvalidReceiptTrigger(t *testing.T) {
	d := Document{BusinessProfile: testProfile()}
	d.Recognition.TriggerMode = "invalid"
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("invalid receipt trigger accepted")
	}
}

func TestLoadParsesLedgerFieldIDs(t *testing.T) {
	d := Document{BusinessProfile: testProfile(), Feishu: FeishuSettings{AppCredentials: AppCredentials{AppID: "app", AppSecret: "secret"}}}
	d.Recognition.Provider = "model"
	d.Model = ModelSettings{APIKey: "key", Name: "model"}
	d.BusinessProfile.Tables.InvoiceLedger.Fields["source_key"] = "source-id"
	cfg, err := loadDocument(t, d)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReceiptLedgerTableID != "ledger" || cfg.ReceiptLedgerFieldIDs["source_key"] != "source-id" || cfg.ReceiptModelAPIKey != "key" {
		t.Fatal("single-file model or ledger configuration was lost")
	}
}

func TestLoadRejectsUnconfiguredReceiptProvider(t *testing.T) {
	d := Document{BusinessProfile: testProfile()}
	d.Recognition.Provider = "anyreceipt"
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("recognition accepted without app credentials")
	}
	d.Feishu.AppID, d.Feishu.AppSecret = "app", "secret"
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("recognition accepted without provider credentials")
	}
	d.Anyreceipt.APIKey = "key"
	d.BusinessProfile.Tables.ReimbursementDetails.BaseToken = ""
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("recognition accepted without source Base")
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	for _, value := range []string{"soon", "0s", "-1s"} {
		d := Document{BusinessProfile: testProfile(), Runtime: RuntimeSettings{ShutdownTimeout: value}}
		if _, err := loadDocument(t, d); err == nil {
			t.Fatal("invalid duration accepted")
		}
	}
}

func TestLoadRejectsPartialFeishuCredentials(t *testing.T) {
	for _, feishu := range []FeishuSettings{{AppCredentials: AppCredentials{AppID: "app"}}, {AppCredentials: AppCredentials{AppSecret: "secret"}}} {
		if _, err := loadDocument(t, Document{BusinessProfile: testProfile(), Feishu: feishu}); err == nil {
			t.Fatal("partial application credentials accepted")
		}
	}
}

func TestSealReviewDoesNotRequireLocalRules(t *testing.T) {
	d := Document{BusinessProfile: testProfile()}
	if _, err := loadDocument(t, d); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsOverlappingReviewContextAndResults(t *testing.T) {
	d := Document{BusinessProfile: testProfile()}
	d.Review.ContextFields = map[string]string{"reason": "decision"}
	if _, err := loadDocument(t, d); err == nil {
		t.Fatal("review output overwrote input")
	}
}

func TestOnlySelectedFileSuppliesCredentialsAndRuntime(t *testing.T) {
	for _, key := range []string{"FEISHU_APP_ID", "FEISHU_APP_SECRET", "ANYRECEIPT_API_KEY", "HTTP_ADDR", "REVIEW_RULES_FILE", "BUSINESS_CONFIG_FILE"} {
		t.Setenv(key, "stale-value")
	}
	d := Document{BusinessProfile: testProfile(), Runtime: RuntimeSettings{HTTPAddr: ":9090"}, Feishu: FeishuSettings{AppCredentials: AppCredentials{AppID: "file-app", AppSecret: "file-secret"}}}
	d.Review.RulesFile = "not-present/rules.json" // Seal must not open self-hosted rules.
	cfg, err := loadDocument(t, d)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FeishuAppID != "file-app" || cfg.FeishuAppSecret != "file-secret" || cfg.HTTPAddr != ":9090" || cfg.AnyreceiptAPIKey != "" || cfg.ReviewRulesFile != "not-present/rules.json" {
		t.Fatal("environment affected single-file configuration")
	}
}

func TestMissingFileNeverFallsBackToEnvironment(t *testing.T) {
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("FEISHU_APP_ID", "stale-app")
	if _, err := Load(); err == nil {
		t.Fatal("missing configuration silently fell back")
	}
}

func TestMalformedPrivateConfigurationDoesNotExposeValues(t *testing.T) {
	raw, _ := toml.Marshal(Document{BusinessProfile: testProfile()})
	raw = append([]byte("SECRET_MUST_NOT_APPEAR = \"secret\"\n"), raw...)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil || strings.Contains(err.Error(), "SECRET_MUST_NOT_APPEAR") {
		t.Fatal("invalid private config accepted or leaked")
	}
}

func TestLoadTablesFile_Valid(t *testing.T) {
	dir := t.TempDir()
	tablesPath := filepath.Join(dir, "tables.json")
	validJSON := `{
		"version": 1,
		"name": "unit-test",
		"tables": {
			"transactions": {
				"name": "流水", "source": "webhook", "access": "read_only", "base_token": "base1", "table_id": "tbl_trans",
				"fields": {"transaction_id": "fld_tid"}
			},
			"reimbursement_details": {
				"name": "明细", "source": "employee", "access": "read_write", "base_token": "base1", "table_id": "tbl_details",
				"fields": {"attachment": "fld_att"},
				"context_fields": {"reason": "fld_reason"},
				"result_fields": {"decision": "fld_dec"}
			},
			"invoice_ledger": {
				"name": "台账", "source": "bridge", "access": "read_write", "base_token": "base1", "table_id": "tbl_ledger",
				"fields": {"source_key": "fld_sk", "raw_json": "fld_raw"}
			}
		}
	}`
	if err := os.WriteFile(tablesPath, []byte(validJSON), 0600); err != nil {
		t.Fatal(err)
	}
	tp, err := LoadTablesFile(tablesPath)
	if err != nil {
		t.Fatalf("LoadTablesFile failed: %v", err)
	}
	if tp.Version != 1 || tp.Name != "unit-test" {
		t.Fatalf("unexpected tables profile: %+v", tp)
	}
	if tp.Tables.Transactions.TableID != "tbl_trans" || tp.Tables.ReimbursementDetails.TableID != "tbl_details" {
		t.Fatalf("unexpected tables bindings")
	}
	if tp.Tables.ReimbursementDetails.ContextFields["reason"] != "fld_reason" || tp.Tables.ReimbursementDetails.ResultFields["decision"] != "fld_dec" {
		t.Fatalf("context or result fields not loaded")
	}
}

func TestLoadTablesFile_DisallowsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	tablesPath := filepath.Join(dir, "tables.json")
	jsonWithUnknown := `{
		"version": 1,
		"name": "unit-test",
		"unknown_field": "disallowed",
		"tables": {
			"transactions": {"name": "流水", "source": "webhook", "access": "read_only", "base_token": "base1", "table_id": "tbl_trans", "fields": {"transaction_id": "fld_tid"}},
			"reimbursement_details": {"name": "明细", "source": "employee", "access": "read_write", "base_token": "base1", "table_id": "tbl_details", "fields": {"attachment": "fld_att"}},
			"invoice_ledger": {"name": "台账", "source": "bridge", "access": "read_write", "base_token": "base1", "table_id": "tbl_ledger", "fields": {"source_key": "fld_sk", "raw_json": "fld_raw"}}
		}
	}`
	if err := os.WriteFile(tablesPath, []byte(jsonWithUnknown), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTablesFile(tablesPath); err == nil {
		t.Fatal("unknown field in tables JSON was accepted")
	}
}

func TestLoadFile_RequiresTablesFile(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	tomlContent := `
[runtime]
http_addr = ":8080"
[recognition]
provider = "disabled"
trigger_mode = "both"
[review]
provider = "seal"
trigger_mode = "manual"
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(tomlPath); err == nil || !strings.Contains(err.Error(), "tables_file is required") {
		t.Fatalf("expected tables_file is required error, got: %v", err)
	}
}

func TestLoadFile_RejectsTablesInTOML(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	tomlContent := `
tables_file = "nonexistent.json"
[tables.transactions]
name = "流水"
`
	if err := os.WriteFile(tomlPath, []byte(tomlContent), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(tomlPath); err == nil {
		t.Fatal("expected rejection of [tables] in TOML, but passed")
	}
}

