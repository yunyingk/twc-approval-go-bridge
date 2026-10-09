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
	Version       int                 `json:"version,omitempty" toml:"version,omitempty"`
	Name          string              `json:"name,omitempty" toml:"name,omitempty"`
	TablesFile    string              `json:"tables_file" toml:"tables_file"`
	Recognition   RecognitionSettings `json:"recognition,omitempty" toml:"recognition,omitempty"`
	RecognitionCN RecognitionSettings `json:"-" toml:"海外小票附件识别,omitempty"`
	Review        ReviewSettings      `json:"review,omitempty" toml:"review,omitempty"`
	ReviewCN      ReviewSettings      `json:"-" toml:"单据AI审批,omitempty"`
	Runtime       RuntimeSettings     `json:"runtime" toml:"runtime"`
	Feishu        FeishuSettings      `json:"feishu" toml:"feishu"`
	Anyreceipt    AnyreceiptSettings  `json:"anyreceipt" toml:"anyreceipt"`
	Seal          SealSettings        `json:"seal" toml:"seal"`
	Model         ModelSettings       `json:"model" toml:"model"`
	Transactions  TransactionSettings `json:"transactions,omitempty" toml:"transactions,omitempty"`

	BusinessProfile BusinessProfile `json:"-" toml:"-"`
}

type TransactionSettings struct {
	AutoNotify   bool   `json:"auto_notify,omitempty" toml:"auto_notify,omitempty"`
	PollInterval string `json:"poll_interval,omitempty" toml:"poll_interval,omitempty"`
}

type RuntimeSettings struct {
	HTTPAddr        string `json:"http_addr" toml:"http_addr"`
	LogLevel        string `json:"log_level" toml:"log_level"`
	ShutdownTimeout string `json:"shutdown_timeout" toml:"shutdown_timeout"`
	StateDir          string `json:"state_dir" toml:"state_dir"`
	DashboardPassword string `json:"dashboard_password,omitempty" toml:"dashboard_password,omitempty"`
}

type AppCredentials struct {
	AppID     string `json:"app_id" toml:"app_id"`
	AppSecret string `json:"app_secret" toml:"app_secret"`
}

type FeishuSettings struct {
	AppCredentials
	Host           string `json:"host,omitempty" toml:"host,omitempty"`
	EnterpriseHost string `json:"enterprise_host,omitempty" toml:"enterprise_host,omitempty"`
	EventType      string `json:"event_type" toml:"event_type"`
	LogRawEvents   bool   `json:"log_raw_events" toml:"log_raw_events"`
}

// NormalizedHost returns the normalized base URL of the Feishu tenant host,
// ensuring https:// scheme and stripping trailing slashes (e.g. "https://zyt-test.feishu.cn").
func (s FeishuSettings) NormalizedHost() string {
	raw := strings.TrimSpace(s.Host)
	if raw == "" {
		raw = strings.TrimSpace(s.EnterpriseHost)
	}
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}


type AnyreceiptSettings struct {
	APIKey string `json:"api_key" toml:"api_key"`
}
type SealSettings struct {
	BaseURL       string `json:"base_url,omitempty" toml:"base_url,omitempty"`
	WebhookID     string `json:"webhook_id,omitempty" toml:"webhook_id,omitempty"`
	DocumentURL   string `json:"document_url,omitempty" toml:"document_url,omitempty"`
	BearerToken   string `json:"bearer_token" toml:"bearer_token"`
	CallbackToken string `json:"callback_token" toml:"callback_token"`
}

type ModelSettings struct {
	APIKey  string `json:"api_key" toml:"api_key"`
	BaseURL string `json:"base_url" toml:"base_url"`
	Name    string `json:"name" toml:"name"`
}

type RuntimeConfig struct {
	HTTPAddr        string
	LogLevel        slog.Level
	ShutdownTimeout   time.Duration
	StateDir          string
	DashboardPassword string
}

type TransactionConfig struct {
	AutoNotify   bool
	PollInterval time.Duration
}

// Config contains runtime settings for the service shell.
type Config struct {
	ConfigFile          string
	TablesFile          string
	Runtime             RuntimeConfig
	Feishu              FeishuSettings
	Anyreceipt          AnyreceiptSettings
	Seal                SealSettings
	Model               ModelSettings
	Transactions        TransactionConfig
	Business            *BusinessProfile
	ReceiptPollInterval time.Duration
	ReceiptPollStartup  string
}

// Load selects a single file. CONFIG_FILE selects its path, never field overrides.
func Load() (Config, error) {
	path := strings.TrimSpace(os.Getenv("CONFIG_FILE"))
	if path == "" {
		path = "configs/config.toml"
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
	if document.Recognition == (RecognitionSettings{}) && document.RecognitionCN != (RecognitionSettings{}) {
		document.Recognition = document.RecognitionCN
	}
	if document.Review.Provider == "" && document.ReviewCN.Provider != "" {
		document.Review = document.ReviewCN
	}
	if strings.TrimSpace(document.Seal.DocumentURL) == "" && strings.TrimSpace(document.Seal.WebhookID) != "" {
		host := strings.TrimSpace(document.Seal.BaseURL)
		if host == "" {
			return Config{}, fmt.Errorf("seal.base_url is required when seal.webhook_id is set")
		}
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "https://" + host
		}
		host = strings.TrimRight(host, "/")
		webhookID := strings.Trim(strings.TrimSpace(document.Seal.WebhookID), "/")
		document.Seal.DocumentURL = fmt.Sprintf("%s/api/v1/integrations/webhook/%s/document", host, webhookID)
	} else if strings.TrimSpace(document.Seal.DocumentURL) == "" && strings.TrimSpace(document.Seal.BaseURL) != "" {
		return Config{}, fmt.Errorf("seal.webhook_id is required when seal.base_url is set")
	}
	if document.Version != 0 && document.Version != 1 {
		return Config{}, fmt.Errorf("configuration version must be 1")
	}
	if strings.TrimSpace(document.TablesFile) == "" {
		return Config{}, fmt.Errorf("tables_file is required")
	}
	tablesPath := document.TablesFile
	if !filepath.IsAbs(tablesPath) {
		// 相对路径解析双通道兜底：
		// 1. 优先尝试相对于当前工作目录（CWD），支持从项目根目录启动（如 tables_file = "configs/tables/xxx.json"）
		// 2. 若当前工作目录不存在该文件，则回退为相对于主 TOML 配置文件所在的目录拼接解析
		if _, err := os.Stat(tablesPath); err != nil {
			tablesPath = filepath.Join(filepath.Dir(path), tablesPath)
		}
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
	txPoll, err := parseDuration("transactions.poll_interval", document.Transactions.PollInterval, 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	level, err := parseLogLevel(document.Runtime.LogLevel)
	if err != nil {
		return Config{}, err
	}
	feishu := document.Feishu
	feishu.Host = feishu.NormalizedHost()
	feishu.EventType = fallback(feishu.EventType, "drive.file.bitable_record_changed_v1")
	cfg := Config{
		ConfigFile: path,
		TablesFile: tablesPath,
		Runtime: RuntimeConfig{
			HTTPAddr:        fallback(document.Runtime.HTTPAddr, ":8080"),
			LogLevel:        level,
			ShutdownTimeout:   shutdown,
			StateDir:          fallback(document.Runtime.StateDir, "data"),
			DashboardPassword: strings.TrimSpace(document.Runtime.DashboardPassword),
		},
		Feishu:              feishu,
		Anyreceipt:          document.Anyreceipt,
		Seal:                document.Seal,
		Model:               document.Model,
		Transactions: TransactionConfig{
			AutoNotify:   document.Transactions.AutoNotify,
			PollInterval: txPoll,
		},
		Business:            &document.BusinessProfile,
		ReceiptPollInterval: poll,
		ReceiptPollStartup:  fallback(document.Recognition.PollStartup, "baseline"),
	}
	if (document.Feishu.AppID == "") != (document.Feishu.AppSecret == "") {
		return Config{}, fmt.Errorf("Feishu app_id and app_secret must be set together")
	}
	contextFields, resultFields := cfg.Business.Review.ContextFields, cfg.Business.Review.ResultFields
	for semantic := range resultFields {
		switch semantic {
		case "decision", "comment", "document_id", "revision", "provider", "external_id", "url":
		default:
			return Config{}, fmt.Errorf("unsupported review result field %q", semantic)
		}
	}
	detail := cfg.Business.Tables.ReimbursementDetails
	ledger := cfg.Business.Tables.InvoiceLedger
	if cfg.ReceiptProvider() != "" {
		sourceKey := ledger.Fields["bridge_attachment_key"]
		if sourceKey == "" {
			sourceKey = ledger.Fields["attachment_key"]
		}
		if sourceKey == "" {
			sourceKey = ledger.Fields["bridge_source_key"]
		}
		if sourceKey == "" {
			sourceKey = ledger.Fields["source_key"]
		}
		rawJSON := ledger.Fields["bridge_raw_json"]
		if rawJSON == "" {
			rawJSON = ledger.Fields["raw_json"]
		}
		if ledger.TableID != "" && (sourceKey == "" || rawJSON == "") {
			return Config{}, fmt.Errorf("ledger writing requires attachment_key and raw_json field IDs")
		}
		switch cfg.ReceiptTriggerMode() {
		case "event", "poll", "both":
		default:
			return Config{}, fmt.Errorf("recognition.trigger_mode must be event, poll or both")
		}
		switch cfg.ReceiptPollStartup {
		case "baseline", "process":
		default:
			return Config{}, fmt.Errorf("recognition.poll_startup must be baseline or process")
		}
		if !cfg.FeishuEnabled() || detail.BaseToken == "" || detail.TableID == "" || detail.AttachmentField() == "" {
			return Config{}, fmt.Errorf("receipt recognition requires Feishu credentials and Base, Table and attachment field IDs")
		}
		switch cfg.ReceiptProvider() {
		case "anyreceipt":
			if cfg.Anyreceipt.APIKey == "" {
				return Config{}, fmt.Errorf("anyreceipt.api_key is required")
			}
		case "model":
			if cfg.Model.APIKey == "" || cfg.Model.Name == "" {
				return Config{}, fmt.Errorf("receipt model API key and name are required")
			}
		default:
			return Config{}, fmt.Errorf("unsupported recognition.provider %q", cfg.ReceiptProvider())
		}
	}
	if cfg.ReviewProvider() != "seal" && cfg.ReviewProvider() != "model" {
		return Config{}, fmt.Errorf("review.provider must be seal or model")
	}
	if cfg.ReviewTriggerMode() != "manual" && cfg.ReviewTriggerMode() != "after_recognition" {
		return Config{}, fmt.Errorf("review.trigger_mode must be manual or after_recognition")
	}
	if cfg.ReviewTriggerMode() == "after_recognition" {
		if cfg.ReceiptProvider() == "" || ledger.TableID == "" {
			return Config{}, fmt.Errorf("automatic review requires receipt recognition and invoice-ledger delivery")
		}
		if resultFields["decision"] == "" || resultFields["document_id"] == "" || resultFields["revision"] == "" {
			return Config{}, fmt.Errorf("automatic review requires dedicated decision, document_id and revision result fields")
		}
		if cfg.ReviewProvider() == "seal" && cfg.Seal.CallbackToken == "" {
			return Config{}, fmt.Errorf("automatic Seal review requires the configured result callback")
		}
	}
	if cfg.Seal.CallbackToken != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{32,}$`).MatchString(cfg.Seal.CallbackToken) {
		return Config{}, fmt.Errorf("seal.callback_token must have at least 32 URL-safe characters")
	}
	for _, id := range resultFields {
		for _, contextID := range contextFields {
			if id == contextID {
				return Config{}, fmt.Errorf("review context and result fields must not overlap")
			}
		}
		if id == detail.Fields["attachment"] || id == detail.Fields["detail_id"] {
			return Config{}, fmt.Errorf("review result fields must not overwrite source attachment or detail ID")
		}
	}
	// Provider credentials are checked when that capability is constructed.
	// Seal never requires a local rules file.
	return cfg, nil
}

// FeishuEnabled reports whether the optional long-connection client is configured.
func (c Config) FeishuEnabled() bool {
	return c.Feishu.AppID != "" && c.Feishu.AppSecret != ""
}

func (c Config) ReceiptProvider() string {
	if c.Business == nil || c.Business.Recognition.Provider == "disabled" {
		return ""
	}
	return c.Business.Recognition.Provider
}

func (c Config) ReceiptTriggerMode() string {
	if c.Business == nil {
		return ""
	}
	return c.Business.Recognition.TriggerMode
}

func (c Config) ReviewProvider() string {
	if c.Business == nil {
		return ""
	}
	return c.Business.Review.Provider
}

func (c Config) ReviewTriggerMode() string {
	if c.Business == nil {
		return ""
	}
	return c.Business.Review.TriggerMode
}

func (c Config) ReviewRulesFile() string {
	if c.Business == nil {
		return ""
	}
	return c.Business.Review.RulesFile
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
