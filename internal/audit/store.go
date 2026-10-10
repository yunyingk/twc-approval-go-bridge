package audit

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Store defines persistent audit logging and querying capabilities.
type Store interface {
	Record(ctx context.Context, entry Entry) error
	Query(ctx context.Context, filter QueryFilter) ([]Entry, int, error)
	Timeline(ctx context.Context, recordID string) ([]Entry, error)
}

// FileStore persists audit entries to a durable append-only jsonl file.
type FileStore struct {
	mu      sync.RWMutex
	dir     string
	logPath string
}

var (
	defaultStoreMu sync.RWMutex
	defaultStore   Store = &noopStore{}
)

// SetDefault sets the global default audit store.
func SetDefault(s Store) {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()
	if s == nil {
		defaultStore = &noopStore{}
		return
	}
	defaultStore = s
}

// Default returns the global default audit store.
func Default() Store {
	defaultStoreMu.RLock()
	defer defaultStoreMu.RUnlock()
	return defaultStore
}

// Log records an audit entry to the default store. Errors are ignored if no store is active.
func Log(entry Entry) {
	_ = Default().Record(context.Background(), entry)
}

// LogCtx records an audit entry to the default store with a context.
func LogCtx(ctx context.Context, entry Entry) error {
	return Default().Record(ctx, entry)
}

// InitFileStore creates and sets a FileStore as default using the specified base directory.
func InitFileStore(stateDir string) (*FileStore, error) {
	if stateDir == "" {
		stateDir = "data/state"
	}
	auditDir := filepath.Join(stateDir, "audit")
	if err := os.MkdirAll(auditDir, 0700); err != nil {
		return nil, fmt.Errorf("create audit directory: %w", err)
	}

	fs := &FileStore{
		dir:     auditDir,
		logPath: filepath.Join(auditDir, "audit.jsonl"),
	}
	SetDefault(fs)
	return fs, nil
}

// Record persists a single audit entry.
func (s *FileStore) Record(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = generateEventID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock audit log: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// Query searches the audit log according to the provided filter.
func (s *FileStore) Query(ctx context.Context, filter QueryFilter) ([]Entry, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := s.readAll(ctx)
	if err != nil {
		return nil, 0, err
	}

	var filtered []Entry
	for _, e := range entries {
		if !matches(e, filter) {
			continue
		}
		filtered = append(filtered, e)
	}

	// 逆序排序 (最新在前)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	total := len(filtered)
	if filter.Offset > 0 {
		if filter.Offset >= len(filtered) {
			return []Entry{}, total, nil
		}
		filtered = filtered[filter.Offset:]
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, total, nil
}

// Timeline returns all lifecycle entries for a specific record in chronological order (earliest to latest).
func (s *FileStore) Timeline(ctx context.Context, recordID string) ([]Entry, error) {
	if recordID == "" {
		return nil, errors.New("record_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := s.readAll(ctx)
	if err != nil {
		return nil, err
	}

	var matched []Entry
	for _, e := range entries {
		if e.RecordID == recordID || e.RelatedID == recordID {
			matched = append(matched, e)
		}
	}

	// 按发生时间正序排序 (从早到晚)
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Timestamp.Before(matched[j].Timestamp)
	})

	return matched, nil
}

func (s *FileStore) readAll(ctx context.Context) ([]Entry, error) {
	f, err := os.Open(s.logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return nil, fmt.Errorf("lock audit log for read: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	var entries []Entry
	scanner := bufio.NewScanner(f)
	// 允许单行最大 2MB (适配较大详细元数据)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // 容错跳过残损行
		}
		entries = append(entries, e)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan audit log: %w", err)
	}
	return entries, nil
}

func matches(e Entry, f QueryFilter) bool {
	if f.RecordID != "" && e.RecordID != f.RecordID && e.RelatedID != f.RecordID {
		return false
	}
	if f.Type != "" {
		target := strings.ToLower(f.Type)
		actual := strings.ToLower(string(e.Type))
		if !strings.Contains(actual, target) {
			return false
		}
	}
	if f.Operator != "" {
		target := strings.ToLower(f.Operator)
		op := strings.ToLower(e.Operator)
		name := strings.ToLower(e.OperatorName)
		if !strings.Contains(op, target) && !strings.Contains(name, target) {
			return false
		}
	}
	if !f.Since.IsZero() && e.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && e.Timestamp.After(f.Until) {
		return false
	}
	return true
}

func generateEventID() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("evt-%d-%s", time.Now().Unix(), hex.EncodeToString(buf[:]))
}

type noopStore struct{}

func (n *noopStore) Record(context.Context, Entry) error                   { return nil }
func (n *noopStore) Query(context.Context, QueryFilter) ([]Entry, int, error) { return nil, 0, nil }
func (n *noopStore) Timeline(context.Context, string) ([]Entry, error)       { return nil, nil }
