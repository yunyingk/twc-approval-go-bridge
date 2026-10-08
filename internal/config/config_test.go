package config

import (
	"github.com/pelletier/go-toml/v2"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeDocument(t *testing.T, document Document) string {
	t.Helper()
	raw, err := toml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
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
	d.Tables.InvoiceLedger.Fields["source_key"] = "source-id"
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
	d.Tables.ReimbursementDetails.BaseToken = ""
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
