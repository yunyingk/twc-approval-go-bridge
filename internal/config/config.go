// Package config loads process configuration from environment variables and business files.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
)

// Config contains runtime settings for the service shell.
type Config struct {
	BusinessConfigFile         string
	Business                   *BusinessProfile
	HTTPAddr                   string
	LogLevel                   slog.Level
	ShutdownTimeout            time.Duration
	FeishuAppID                string
	FeishuAppSecret            string
	FeishuApprovalAppID        string
	FeishuApprovalAppSecret    string
	FeishuEventType            string
	FeishuLogRawEvents         bool
	ReceiptBaseToken           string
	ReceiptTableID             string
	ReceiptFieldID             string
	ReceiptProvider            string
	ReceiptTriggerMode         string
	ReceiptPollInterval        time.Duration
	ReceiptPollStartup         string
	AnyreceiptAPIKey           string
	ReceiptModelAPIKey         string
	ReceiptModelBaseURL        string
	ReceiptModelName           string
	ReceiptLedgerTableID       string
	ReceiptSourceDetailFieldID string
	ReceiptLedgerFieldIDs      map[string]string
	SealDocumentURL            string
	StateDir                   string
	SealCallbackToken          string
	ReviewProvider             string
	ReviewTriggerMode          string
	ReviewModelAPIKey          string
	ReviewModelBaseURL         string
	ReviewModelName            string
	ReviewRulesFile            string
	ReviewContextFieldIDs      map[string]string
	ReviewResultFieldIDs       map[string]string
	SealBearerToken            string
}

// Load reads configuration from the environment and applies development-safe defaults.
func Load() (Config, error) {
	shutdownTimeout, err := duration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	level, err := logLevel("LOG_LEVEL", slog.LevelInfo)
	if err != nil {
		return Config{}, err
	}

	feishuAppID := value("FEISHU_APP_ID", "")
	feishuAppSecret := value("FEISHU_APP_SECRET", "")
	if (feishuAppID == "") != (feishuAppSecret == "") {
		return Config{}, fmt.Errorf("FEISHU_APP_ID and FEISHU_APP_SECRET must be set together")
	}

	logRawEvents, err := boolean("FEISHU_LOG_RAW_EVENTS", false)
	if err != nil {
		return Config{}, err
	}
	pollInterval, err := duration("RECEIPT_POLL_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	businessFile := value("BUSINESS_CONFIG_FILE", "")
	var business *BusinessProfile
	var ledgerFieldIDs, contextFields, resultFields map[string]string
	if businessFile != "" {
		business, err = LoadBusinessProfile(businessFile)
		if err != nil {
			return Config{}, err
		}
	} else {
		// Legacy bindings remain available only when no complete profile is selected.
		// Never mix field IDs from two environments, even when old variables remain set.
		ledgerFieldIDs, err = fieldMapping("RECEIPT_LEDGER_FIELD_IDS")
		if err != nil {
			return Config{}, err
		}
		contextFields, err = fieldMapping("REVIEW_CONTEXT_FIELD_IDS")
		if err != nil {
			return Config{}, err
		}
		resultFields, err = fieldMapping("REVIEW_RESULT_FIELD_IDS")
		if err != nil {
			return Config{}, err
		}
	}
	cfg := Config{
		BusinessConfigFile:         businessFile,
		Business:                   business,
		HTTPAddr:                   value("HTTP_ADDR", ":8080"),
		LogLevel:                   level,
		ShutdownTimeout:            shutdownTimeout,
		FeishuAppID:                feishuAppID,
		FeishuAppSecret:            feishuAppSecret,
		FeishuApprovalAppID:        value("FEISHU_APPROVAL_APP_ID", ""),
		FeishuApprovalAppSecret:    value("FEISHU_APPROVAL_APP_SECRET", ""),
		FeishuEventType:            value("FEISHU_EVENT_TYPE", "drive.file.bitable_record_changed_v1"),
		FeishuLogRawEvents:         logRawEvents,
		ReceiptBaseToken:           value("RECEIPT_BASE_TOKEN", ""),
		ReceiptTableID:             value("RECEIPT_TABLE_ID", ""),
		ReceiptFieldID:             value("RECEIPT_ATTACHMENT_FIELD_ID", ""),
		ReceiptProvider:            strings.ToLower(value("RECEIPT_PROVIDER", "")),
		ReceiptTriggerMode:         strings.ToLower(value("RECEIPT_TRIGGER_MODE", "both")),
		ReceiptPollInterval:        pollInterval,
		ReceiptPollStartup:         strings.ToLower(value("RECEIPT_POLL_STARTUP", "baseline")),
		AnyreceiptAPIKey:           value("ANYRECEIPT_API_KEY", ""),
		ReceiptModelAPIKey:         value("RECEIPT_MODEL_API_KEY", ""),
		ReceiptModelBaseURL:        value("RECEIPT_MODEL_BASE_URL", ""),
		ReceiptModelName:           value("RECEIPT_MODEL_NAME", ""),
		ReceiptLedgerTableID:       value("RECEIPT_LEDGER_TABLE_ID", ""),
		ReceiptSourceDetailFieldID: value("RECEIPT_SOURCE_DETAIL_FIELD_ID", ""),
		ReceiptLedgerFieldIDs:      ledgerFieldIDs,
		SealDocumentURL:            value("SEAL_DOCUMENT_URL", ""),
		SealCallbackToken:          value("SEAL_CALLBACK_TOKEN", ""),
		StateDir:                   value("STATE_DIR", "data"),
		ReviewProvider:             strings.ToLower(value("REVIEW_PROVIDER", "seal")),
		ReviewTriggerMode:          strings.ToLower(value("REVIEW_TRIGGER_MODE", "manual")),
		ReviewModelAPIKey:          value("REVIEW_MODEL_API_KEY", ""),
		ReviewModelBaseURL:         value("REVIEW_MODEL_BASE_URL", ""),
		ReviewModelName:            value("REVIEW_MODEL_NAME", ""),
		ReviewRulesFile:            value("REVIEW_RULES_FILE", ""),
		ReviewContextFieldIDs:      contextFields, ReviewResultFieldIDs: resultFields,
		SealBearerToken: value("SEAL_BEARER_TOKEN", ""),
	}
	if business != nil {
		business.apply(&cfg)
	}
	if cfg.ApprovalObservationEnabled() {
		if _, _, err := cfg.ApprovalObservationCredentials(); err != nil {
			return Config{}, err
		}
	}
	contextFields, resultFields = cfg.ReviewContextFieldIDs, cfg.ReviewResultFieldIDs
	for semantic := range resultFields {
		switch semantic {
		case "decision", "comment", "document_id", "revision", "provider", "external_id", "url":
		default:
			return Config{}, fmt.Errorf("unsupported review result field %q", semantic)
		}
	}
	if cfg.ReceiptProvider != "" {
		if cfg.ReceiptLedgerTableID != "" && (cfg.ReceiptLedgerFieldIDs["source_key"] == "" || cfg.ReceiptLedgerFieldIDs["raw_json"] == "") {
			return Config{}, fmt.Errorf("ledger writing requires source_key and raw_json field IDs")
		}
		switch cfg.ReceiptTriggerMode {
		case "event", "poll", "both":
		default:
			return Config{}, fmt.Errorf("RECEIPT_TRIGGER_MODE must be event, poll or both")
		}
		switch cfg.ReceiptPollStartup {
		case "baseline", "process":
		default:
			return Config{}, fmt.Errorf("RECEIPT_POLL_STARTUP must be baseline or process")
		}
		if !cfg.FeishuEnabled() || cfg.ReceiptBaseToken == "" || cfg.ReceiptTableID == "" || cfg.ReceiptFieldID == "" {
			return Config{}, fmt.Errorf("receipt recognition requires Feishu credentials and Base, Table and attachment field IDs")
		}
		switch cfg.ReceiptProvider {
		case "anyreceipt":
			if cfg.AnyreceiptAPIKey == "" {
				return Config{}, fmt.Errorf("ANYRECEIPT_API_KEY is required")
			}
		case "model":
			if cfg.ReceiptModelAPIKey == "" || cfg.ReceiptModelName == "" {
				return Config{}, fmt.Errorf("receipt model API key and name are required")
			}
		default:
			return Config{}, fmt.Errorf("unsupported RECEIPT_PROVIDER %q", cfg.ReceiptProvider)
		}
	}
	if cfg.ReviewProvider != "seal" && cfg.ReviewProvider != "model" {
		return Config{}, fmt.Errorf("REVIEW_PROVIDER must be seal or model")
	}
	if cfg.ReviewTriggerMode != "manual" && cfg.ReviewTriggerMode != "after_recognition" {
		return Config{}, fmt.Errorf("REVIEW_TRIGGER_MODE must be manual or after_recognition")
	}
	if cfg.ReviewTriggerMode == "after_recognition" {
		if cfg.ReceiptProvider == "" || cfg.ReceiptLedgerTableID == "" {
			return Config{}, fmt.Errorf("automatic review requires receipt recognition and invoice-ledger delivery")
		}
		if resultFields["decision"] == "" || resultFields["document_id"] == "" || resultFields["revision"] == "" {
			return Config{}, fmt.Errorf("automatic review requires dedicated decision, document_id and revision result fields")
		}
		if cfg.ReviewProvider == "seal" && cfg.SealCallbackToken == "" {
			return Config{}, fmt.Errorf("automatic Seal review requires the configured result callback")
		}
	}
	if cfg.SealCallbackToken != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{32,}$`).MatchString(cfg.SealCallbackToken) {
		return Config{}, fmt.Errorf("SEAL_CALLBACK_TOKEN must have at least 32 URL-safe characters")
	}
	for _, id := range resultFields {
		for _, contextID := range contextFields {
			if id == contextID {
				return Config{}, fmt.Errorf("review context and result fields must not overlap")
			}
		}
		if id == cfg.ReceiptFieldID || id == cfg.ReceiptSourceDetailFieldID {
			return Config{}, fmt.Errorf("review result fields must not overwrite source attachment or detail ID")
		}
	}
	// Provider credentials are checked when that capability is constructed.
	// Seal never requires a local rules file.
	return cfg, nil
}

// FeishuEnabled reports whether the optional long-connection client is configured.
func (c Config) FeishuEnabled() bool {
	return c.FeishuAppID != "" && c.FeishuAppSecret != ""
}

func value(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	v := value(key, fallback.String())
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: parse duration %q: %w", key, v, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s: duration must be positive", key)
	}
	return parsed, nil
}

func logLevel(key string, fallback slog.Level) (slog.Level, error) {
	v := strings.ToLower(value(key, fallback.String()))
	switch v {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%s: unsupported log level %q", key, v)
	}
}

func boolean(key string, fallback bool) (bool, error) {
	v := strings.ToLower(value(key, ""))
	if v == "" {
		return fallback, nil
	}
	switch v {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s: unsupported boolean value %q", key, v)
	}
}

func fieldMapping(key string) (map[string]string, error) {
	var fields map[string]string
	if raw := value(key, ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return nil, fmt.Errorf("%s: invalid field mapping", key)
		}
		if err := validateFieldMapping(key, fields); err != nil {
			return nil, err
		}
	}
	return fields, nil
}
