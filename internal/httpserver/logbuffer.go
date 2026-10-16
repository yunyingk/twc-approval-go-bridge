package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SystemLogEntry represents a captured log line from the server slog handler.
type SystemLogEntry struct {
	ID      int64          `json:"id"`
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// LogBuffer maintains a thread-safe circular memory buffer for live server logs.
type LogBuffer struct {
	mu      sync.RWMutex
	entries []SystemLogEntry
	maxSize int
	nextID  int64
}

// NewLogBuffer initializes a circular buffer with a maximum capacity.
func NewLogBuffer(maxSize int) *LogBuffer {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &LogBuffer{
		entries: make([]SystemLogEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add appends a log entry to the buffer.
func (b *LogBuffer) Add(t time.Time, level slog.Level, msg string, attrs map[string]any) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	id := atomic.AddInt64(&b.nextID, 1)
	entry := SystemLogEntry{
		ID:      id,
		Time:    t,
		Level:   level.String(),
		Message: msg,
		Attrs:   attrs,
	}

	if len(b.entries) >= b.maxSize {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = entry
	} else {
		b.entries = append(b.entries, entry)
	}
}

// LogFilterOptions specifies filters and ordering for querying system logs.
type LogFilterOptions struct {
	Limit    int
	MinLevel string
	SinceID  int64
	Keyword  string
	Order    string // "asc" (default) or "desc"
}

// FormatLine returns a concise single-line representation of the log entry.
func (e SystemLogEntry) FormatLine() string {
	ts := e.Time.In(CSTZone).Format("2006-01-02 15:04:05.000")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s [%s] %s", ts, e.Level, e.Message))
	if len(e.Attrs) > 0 {
		keys := make([]string, 0, len(e.Attrs))
		for k := range e.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf(" %s=%v", k, e.Attrs[k]))
		}
	}
	return sb.String()
}

// Query retrieves logs based on limit, minimum level, and optional sinceID for incremental polling.
func (b *LogBuffer) Query(limit int, minLevel string, sinceID int64) []SystemLogEntry {
	return b.QueryExtended(LogFilterOptions{
		Limit:    limit,
		MinLevel: minLevel,
		SinceID:  sinceID,
		Order:    "asc",
	})
}

// QueryExtended provides powerful search, filtering and sorting across the circular log buffer.
func (b *LogBuffer) QueryExtended(opt LogFilterOptions) []SystemLogEntry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	limit := opt.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	var parsedMinLevel slog.Level
	hasMinLevel := false
	if opt.MinLevel != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(strings.ToUpper(opt.MinLevel))); err == nil {
			parsedMinLevel = l
			hasMinLevel = true
		}
	}

	kw := strings.ToLower(strings.TrimSpace(opt.Keyword))
	matchesEntry := func(e SystemLogEntry) bool {
		if opt.SinceID > 0 && e.ID <= opt.SinceID {
			return false
		}
		if hasMinLevel && parseLogLevelStr(e.Level) < parsedMinLevel {
			return false
		}
		if kw != "" {
			if strings.Contains(strings.ToLower(e.Message), kw) {
				return true
			}
			for k, v := range e.Attrs {
				if strings.Contains(strings.ToLower(k), kw) || strings.Contains(strings.ToLower(fmt.Sprint(v)), kw) {
					return true
				}
			}
			return false
		}
		return true
	}

	if strings.EqualFold(opt.Order, "desc") {
		var matched []SystemLogEntry
		for i := len(b.entries) - 1; i >= 0; i-- {
			e := b.entries[i]
			if matchesEntry(e) {
				matched = append(matched, e)
				if len(matched) >= limit {
					break
				}
			}
		}
		return matched
	}

	if opt.SinceID > 0 || kw != "" {
		var matched []SystemLogEntry
		for _, e := range b.entries {
			if matchesEntry(e) {
				matched = append(matched, e)
				if len(matched) >= limit {
					break
				}
			}
		}
		return matched
	}

	start := len(b.entries) - limit
	if start < 0 {
		start = 0
	}
	var matched []SystemLogEntry
	for i := start; i < len(b.entries); i++ {
		e := b.entries[i]
		if matchesEntry(e) {
			matched = append(matched, e)
		}
	}
	return matched
}

func parseLogLevelStr(s string) slog.Level {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// BufferedHandler wraps an underlying slog.Handler to replicate logs to LogBuffer.
type BufferedHandler struct {
	underlying slog.Handler
	buffer     *LogBuffer
}

// NewBufferedHandler creates a new replicating slog handler.
func NewBufferedHandler(underlying slog.Handler, buffer *LogBuffer) *BufferedHandler {
	return &BufferedHandler{
		underlying: underlying,
		buffer:     buffer,
	}
}

func (h *BufferedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.underlying.Enabled(ctx, level)
}

func (h *BufferedHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.buffer != nil {
		attrs := make(map[string]any)
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		h.buffer.Add(r.Time, r.Level, r.Message, attrs)
	}
	return h.underlying.Handle(ctx, r)
}

func (h *BufferedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &BufferedHandler{
		underlying: h.underlying.WithAttrs(attrs),
		buffer:     h.buffer,
	}
}

func (h *BufferedHandler) WithGroup(name string) slog.Handler {
	return &BufferedHandler{
		underlying: h.underlying.WithGroup(name),
		buffer:     h.buffer,
	}
}
