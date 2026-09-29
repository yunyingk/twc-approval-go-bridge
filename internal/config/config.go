// Package config loads process configuration from environment variables.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config contains runtime settings for the service shell.
type Config struct {
	HTTPAddr                   string
	LogLevel                   slog.Level
	ShutdownTimeout            time.Duration
	FeishuAppID                string
	FeishuAppSecret            string
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
	var ledgerFieldIDs map[string]string
	if raw := value("RECEIPT_LEDGER_FIELD_IDS", ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &ledgerFieldIDs); err != nil {
			return Config{}, fmt.Errorf("RECEIPT_LEDGER_FIELD_IDS: %w", err)
		}
	}

	cfg := Config{
		HTTPAddr:                   value("HTTP_ADDR", ":8080"),
		LogLevel:                   level,
		ShutdownTimeout:            shutdownTimeout,
		FeishuAppID:                feishuAppID,
		FeishuAppSecret:            feishuAppSecret,
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
