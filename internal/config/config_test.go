package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	t.Setenv("FEISHU_EVENT_TYPE", "")
	t.Setenv("FEISHU_LOG_RAW_EVENTS", "")
	t.Setenv("RECEIPT_PROVIDER", "")
	t.Setenv("RECEIPT_TRIGGER_MODE", "")
	t.Setenv("RECEIPT_POLL_STARTUP", "")
	t.Setenv("RECEIPT_POLL_INTERVAL", "")
	t.Setenv("REVIEW_TRIGGER_MODE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.FeishuEnabled() {
		t.Error("FeishuEnabled() = true, want false")
	}
	if cfg.FeishuEventType != "drive.file.bitable_record_changed_v1" {
		t.Errorf("FeishuEventType = %q, want default event type", cfg.FeishuEventType)
	}
	if cfg.ReceiptTriggerMode != "both" || cfg.ReceiptPollStartup != "baseline" || cfg.ReceiptPollInterval != 5*time.Minute {
		t.Errorf("unexpected receipt trigger defaults: mode=%q startup=%q interval=%s", cfg.ReceiptTriggerMode, cfg.ReceiptPollStartup, cfg.ReceiptPollInterval)
	}
	if cfg.ReviewTriggerMode != "manual" {
		t.Errorf("ReviewTriggerMode = %q, want manual", cfg.ReviewTriggerMode)
	}
}

func TestAutomaticReviewRequiresRecognitionDeliveryAndResults(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("RECEIPT_BASE_TOKEN", "base")
	t.Setenv("RECEIPT_TABLE_ID", "details")
	t.Setenv("RECEIPT_ATTACHMENT_FIELD_ID", "attachment")
	t.Setenv("ANYRECEIPT_API_KEY", "test")
	t.Setenv("RECEIPT_LEDGER_FIELD_IDS", `{"source_key":"source","raw_json":"raw"}`)
	t.Setenv("REVIEW_PROVIDER", "seal")
	t.Setenv("REVIEW_TRIGGER_MODE", "after_recognition")
	t.Setenv("RECEIPT_PROVIDER", "")
	t.Setenv("RECEIPT_LEDGER_TABLE_ID", "")
	t.Setenv("REVIEW_RESULT_FIELD_IDS", "")
	t.Setenv("SEAL_CALLBACK_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("automatic review accepted disabled receipt recognition")
	}
	t.Setenv("RECEIPT_PROVIDER", "anyreceipt")
	t.Setenv("RECEIPT_LEDGER_TABLE_ID", "ledger")
	if _, err := Load(); err == nil {
		t.Fatal("automatic review accepted missing result columns")
	}
	t.Setenv("REVIEW_RESULT_FIELD_IDS", `{"decision":"decision","document_id":"document","revision":"revision"}`)
	if _, err := Load(); err == nil {
		t.Fatal("automatic Seal review accepted a missing result callback")
	}
	t.Setenv("SEAL_CALLBACK_TOKEN", "abcdefghijklmnopqrstuvwxyz012345")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_TRIGGER_MODE", "unknown")
	if _, err := Load(); err == nil {
		t.Fatal("unknown review trigger accepted")
	}
}

func TestLoadRejectsInvalidReceiptTrigger(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("RECEIPT_PROVIDER", "model")
	t.Setenv("RECEIPT_BASE_TOKEN", "base")
	t.Setenv("RECEIPT_TABLE_ID", "table")
	t.Setenv("RECEIPT_ATTACHMENT_FIELD_ID", "field")
	t.Setenv("RECEIPT_MODEL_API_KEY", "test")
	t.Setenv("RECEIPT_MODEL_NAME", "test")
	t.Setenv("RECEIPT_TRIGGER_MODE", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted invalid receipt trigger mode")
	}
}

func TestLoadParsesLedgerFieldIDs(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("RECEIPT_PROVIDER", "model")
	t.Setenv("RECEIPT_BASE_TOKEN", "base")
	t.Setenv("RECEIPT_TABLE_ID", "detail")
	t.Setenv("RECEIPT_ATTACHMENT_FIELD_ID", "attachment")
	t.Setenv("RECEIPT_MODEL_API_KEY", "test")
	t.Setenv("RECEIPT_MODEL_NAME", "test")
	t.Setenv("RECEIPT_LEDGER_TABLE_ID", "ledger")
	t.Setenv("RECEIPT_LEDGER_FIELD_IDS", `{"source_key":"source-id","raw_json":"raw-id"}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReceiptLedgerTableID != "ledger" || cfg.ReceiptLedgerFieldIDs["source_key"] != "source-id" {
		t.Fatalf("ledger config = %+v", cfg.ReceiptLedgerFieldIDs)
	}
}

func TestLoadRejectsUnconfiguredReceiptProvider(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("RECEIPT_PROVIDER", "model")
	t.Setenv("RECEIPT_BASE_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted receipt provider without target Base")
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadRejectsPartialFeishuCredentials(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want partial credential error")
	}
}

func TestSealReviewDoesNotRequireLocalRules(t *testing.T) {
	t.Setenv("REVIEW_PROVIDER", "seal")
	t.Setenv("REVIEW_RULES_FILE", "")
	t.Setenv("RECEIPT_PROVIDER", "")
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsOverlappingReviewContextAndResults(t *testing.T) {
	t.Setenv("REVIEW_CONTEXT_FIELD_IDS", `{"amount":"field"}`)
	t.Setenv("REVIEW_RESULT_FIELD_IDS", `{"decision":"field"}`)
	if _, err := Load(); err == nil {
		t.Fatal("review output would overwrite its own input")
	}
}
