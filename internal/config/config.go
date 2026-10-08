// Package config loads the bridge from one complete TOML document.
package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Document is the only runtime configuration source, including credentials.
// The private file is not committed; the repository contains a redacted example.
type Document struct {
	Version     int                 `json:"version,omitempty" toml:"version,omitempty"`
	Name        string              `json:"name,omitempty" toml:"name,omitempty"`
	TablesFile  string              `json:"tables_file" toml:"tables_file"`
	Recognition RecognitionSettings `json:"recognition" toml:"recognition"`
	Review      ReviewSettings      `json:"review" toml:"review"`
	Runtime     RuntimeSettings     `json:"runtime" toml:"runtime"`
	Feishu      FeishuSettings      `json:"feishu" toml:"feishu"`
	Anyreceipt  AnyreceiptSettings  `json:"anyreceipt" toml:"anyreceipt"`
	Seal        SealSettings        `json:"seal" toml:"seal"`
	Model       ModelSettings       `json:"model" toml:"model"`

	BusinessProfile BusinessProfile `json:"-" toml:"-"`
}

type RuntimeSettings struct {
	HTTPAddr        string `json:"http_addr" toml:"http_addr"`
	LogLevel        string `json:"log_level" toml:"log_level"`
	ShutdownTimeout string `json:"shutdown_timeout" toml:"shutdown_timeout"`
	StateDir        string `json:"state_dir" toml:"state_dir"`
}

type AppCredentials struct {
	AppID     string `json:"app_id" toml:"app_id"`
	AppSecret string `json:"app_secret" toml:"app_secret"`
}

type FeishuSettings struct {
	AppCredentials
	EventType    string `json:"event_type" toml:"event_type"`
	LogRawEvents bool   `json:"log_raw_events" toml:"log_raw_events"`
}

type AnyreceiptSettings struct {
	APIKey string `json:"api_key" toml:"api_key"`
}
type SealSettings struct {
	DocumentURL   string `json:"document_url" toml:"document_url"`
	BearerToken   string `json:"bearer_token" toml:"bearer_token"`
	CallbackToken string `json:"callback_token" toml:"callback_token"`
}

type ModelSettings struct {
	APIKey  string `json:"api_key" toml:"api_key"`
	BaseURL string `json:"base_url" toml:"base_url"`
	Name    string `json:"name" toml:"name"`
}

// Config contains runtime settings for the service shell.
type Config struct {
	ConfigFile                 string
	TablesFile                 string
	Business                   *BusinessProfile
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

// Load selects a single file. CONFIG_FILE selects its path, never field overrides.
func Load() (Config, error) {
	path := strings.TrimSpace(os.Getenv("CONFIG_FILE"))
	if path == "" {
		path = "config.toml"
	}
	return LoadFile(path)
}

// LoadFile never reads environment credentials, other profiles or rule files.
func LoadFile(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return Config{}, fmt.Errorf("configuration must be a regular file of at most 1 MiB")
	}
	decoder := toml.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		// Do not echo private TOML values or unknown keys into application logs.
		return Config{}, fmt.Errorf("invalid configuration TOML or unknown field")
	}
	if document.Version != 0 && document.Version != 1 {
		return Config{}, fmt.Errorf("configuration version must be 1")
	}
	if strings.TrimSpace(document.TablesFile) == "" {
		return Config{}, fmt.Errorf("tables_file is required")
	}
	tablesPath := document.TablesFile
	if !filepath.IsAbs(tablesPath) {
		tablesPath = filepath.Join(filepath.Dir(path), tablesPath)
	}
	tablesProfile, err := LoadTablesFile(tablesPath)
	if err != nil {
		return Config{}, fmt.Errorf("load tables file: %w", err)
	}
	document.BusinessProfile = BusinessProfile{
		Version: tablesProfile.Version,
		Name:    tablesProfile.Name,
		Tables: BusinessTables{
			Transactions:         tablesProfile.Tables.Transactions,
			ReimbursementDetails: tablesProfile.Tables.ReimbursementDetails.TableBinding(),
			InvoiceLedger:        tablesProfile.Tables.InvoiceLedger,
		},
		Recognition: document.Recognition,
		Review:      document.Review,
	}
	if document.Name != "" {
		document.BusinessProfile.Name = document.Name
	}
	document.BusinessProfile.Review.ContextFields = tablesProfile.Tables.ReimbursementDetails.ContextFields
	document.BusinessProfile.Review.ResultFields = tablesProfile.Tables.ReimbursementDetails.ResultFields

	if err := document.BusinessProfile.validate(); err != nil {
		return Config{}, err
	}
	shutdown, err := parseDuration("runtime.shutdown_timeout", document.Runtime.ShutdownTimeout, 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	poll, err := parseDuration("recognition.poll_interval", document.Recognition.PollInterval, 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	level, err := parseLogLevel(document.Runtime.LogLevel)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ConfigFile: path, TablesFile: tablesPath, Business: &document.BusinessProfile,
		HTTPAddr: fallback(document.Runtime.HTTPAddr, ":8080"), LogLevel: level,
		ShutdownTimeout: shutdown, StateDir: fallback(document.Runtime.StateDir, "data"),
		FeishuAppID:        document.Feishu.AppID,
		FeishuAppSecret:    document.Feishu.AppSecret,
		FeishuEventType:    fallback(document.Feishu.EventType, "drive.file.bitable_record_changed_v1"),
		FeishuLogRawEvents: document.Feishu.LogRawEvents,
		ReceiptPollInterval: poll, ReceiptPollStartup: fallback(document.Recognition.PollStartup, "baseline"),
		AnyreceiptAPIKey:    document.Anyreceipt.APIKey,
		ReceiptModelAPIKey:  document.Model.APIKey,
		ReceiptModelBaseURL: document.Model.BaseURL, ReceiptModelName: document.Model.Name,
		SealDocumentURL: document.Seal.DocumentURL, SealBearerToken: document.Seal.BearerToken,
		SealCallbackToken:  document.Seal.CallbackToken,
		ReviewModelAPIKey:  document.Model.APIKey,
		ReviewModelBaseURL: document.Model.BaseURL, ReviewModelName: document.Model.Name,
		ReviewRulesFile: document.Review.RulesFile,
	}
	document.BusinessProfile.apply(&cfg)
	if (document.Feishu.AppID == "") != (document.Feishu.AppSecret == "") {
		return Config{}, fmt.Errorf("Feishu app_id and app_secret must be set together")
	}
	contextFields, resultFields := cfg.ReviewContextFieldIDs, cfg.ReviewResultFieldIDs
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
			return Config{}, fmt.Errorf("recognition.trigger_mode must be event, poll or both")
		}
		switch cfg.ReceiptPollStartup {
		case "baseline", "process":
		default:
			return Config{}, fmt.Errorf("recognition.poll_startup must be baseline or process")
		}
		if !cfg.FeishuEnabled() || cfg.ReceiptBaseToken == "" || cfg.ReceiptTableID == "" || cfg.ReceiptFieldID == "" {
			return Config{}, fmt.Errorf("receipt recognition requires Feishu credentials and Base, Table and attachment field IDs")
		}
		switch cfg.ReceiptProvider {
		case "anyreceipt":
			if cfg.AnyreceiptAPIKey == "" {
				return Config{}, fmt.Errorf("anyreceipt.api_key is required")
			}
		case "model":
			if cfg.ReceiptModelAPIKey == "" || cfg.ReceiptModelName == "" {
				return Config{}, fmt.Errorf("receipt model API key and name are required")
			}
		default:
			return Config{}, fmt.Errorf("unsupported recognition.provider %q", cfg.ReceiptProvider)
		}
	}
	if cfg.ReviewProvider != "seal" && cfg.ReviewProvider != "model" {
		return Config{}, fmt.Errorf("review.provider must be seal or model")
	}
	if cfg.ReviewTriggerMode != "manual" && cfg.ReviewTriggerMode != "after_recognition" {
		return Config{}, fmt.Errorf("review.trigger_mode must be manual or after_recognition")
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
		return Config{}, fmt.Errorf("seal.callback_token must have at least 32 URL-safe characters")
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

func fallback(input, defaultValue string) string {
	if strings.TrimSpace(input) == "" {
		return defaultValue
	}
	return input
}

func parseDuration(label, input string, defaultValue time.Duration) (time.Duration, error) {
	parsed, err := time.ParseDuration(fallback(input, defaultValue.String()))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", label)
	}
	return parsed, nil
}

func parseLogLevel(input string) (slog.Level, error) {
	switch strings.ToLower(fallback(input, "info")) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("runtime.log_level must be debug, info, warn or error")
	}
}
