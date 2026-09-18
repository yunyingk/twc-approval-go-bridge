// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config contains runtime settings for the service shell.
type Config struct {
	HTTPAddr           string
	LogLevel           slog.Level
	ShutdownTimeout    time.Duration
	FeishuAppID        string
	FeishuAppSecret    string
	FeishuEventType    string
	FeishuLogRawEvents bool
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

	return Config{
		HTTPAddr:           value("HTTP_ADDR", ":8080"),
		LogLevel:           level,
		ShutdownTimeout:    shutdownTimeout,
		FeishuAppID:        feishuAppID,
		FeishuAppSecret:    feishuAppSecret,
		FeishuEventType:    value("FEISHU_EVENT_TYPE", "drive.file.bitable_record_changed_v1"),
		FeishuLogRawEvents: logRawEvents,
	}, nil
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
