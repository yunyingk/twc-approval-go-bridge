// Package state provides single-host durable state with atomic replacement and
// OS file locks. A shared network filesystem is not a distributed task store.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type Files struct{ root string }

// AcquireWorker enforces a single receipt worker per source on this host.
func (s *Files) AcquireWorker(scope string) (*os.File, error) {
	hash := sha256.Sum256([]byte("worker:" + scope))
	lock, err := os.OpenFile(filepath.Join(s.root, hex.EncodeToString(hash[:])+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("receipt source already has a running worker")
	}
	return lock, nil
}

func NewFiles(root string) (*Files, error) {
	if root == "" {
		return nil, fmt.Errorf("state directory is required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &Files{root: root}, nil
}

// Transaction serializes read/modify/write for one key across local processes.
// The OS releases the lock on crash, so restart never needs to delete a stale lock.
func (s *Files) Transaction(ctx context.Context, key string, change func(json.RawMessage) (any, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(hash[:])
	lock, err := os.OpenFile(filepath.Join(s.root, name+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-lockCtx.Done():
			timer.Stop()
			return fmt.Errorf("wait for state lock: %w", lockCtx.Err())
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := filepath.Join(s.root, name+".json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	value, err := change(raw)
	if err != nil {
		return err
	}
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Files) Begin(ctx context.Context, request core.Request) (core.Attempt, bool, error) {
	var attempt core.Attempt
	created := false
	err := s.Transaction(ctx, "review:"+request.Document.DocumentID, func(raw json.RawMessage) (any, error) {
		if len(raw) > 0 {
			return nil, json.Unmarshal(raw, &attempt)
		}
		// Retain facts and evidence; original binary content stays in the source system.
		copyDoc := request.Document
		copyDoc.Invoices = append(copyDoc.Invoices[:0:0], copyDoc.Invoices...)
		for i := range copyDoc.Invoices {
			copyDoc.Invoices[i].Attachment.Data = nil
			copyDoc.Invoices[i].Attachment.URL = ""
		}
		request.Document = copyDoc
		attempt = core.Attempt{Request: request, State: "submitting"}
		created = true
		return attempt, nil
	})
	return attempt, created, err
}
func (s *Files) Update(ctx context.Context, documentID string, update func(*core.Attempt) error) (core.Attempt, error) {
	var attempt core.Attempt
	err := s.Transaction(ctx, "review:"+documentID, func(raw json.RawMessage) (any, error) {
		if len(raw) == 0 {
			return nil, fmt.Errorf("unknown review document")
		}
		if err := json.Unmarshal(raw, &attempt); err != nil {
			return nil, err
		}
		if attempt.Request.Document.DocumentID != documentID {
			return nil, fmt.Errorf("review state identity mismatch")
		}
		if err := update(&attempt); err != nil {
			return nil, err
		}
		return attempt, nil
	})
	return attempt, err
}

func (s *Files) PendingReviews(ctx context.Context) ([]core.Attempt, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var attempts []core.Attempt
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		var attempt core.Attempt
		if err := json.Unmarshal(raw, &attempt); err != nil {
			return nil, err
		}
		if attempt.State == "completed" && !attempt.Delivered && attempt.Submission != nil && attempt.Submission.Outcome != nil {
			attempts = append(attempts, attempt)
		}
	}
	return attempts, nil
}
