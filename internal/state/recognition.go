package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
)

func (s *Files) QueueRecognition(ctx context.Context, task recognition.Checkpoint) error {
	return s.Transaction(ctx, "receipt:"+task.Key, func(raw json.RawMessage) (any, error) {
		if len(raw) > 0 {
			return nil, nil
		}
		return task, nil
	})
}
func (s *Files) LoadRecognition(ctx context.Context, key string) (recognition.Checkpoint, error) {
	var task recognition.Checkpoint
	err := s.Transaction(ctx, "receipt:"+key, func(raw json.RawMessage) (any, error) {
		if len(raw) == 0 {
			return nil, fmt.Errorf("unknown receipt task")
		}
		return nil, json.Unmarshal(raw, &task)
	})
	return task, err
}
func (s *Files) SaveRecognition(ctx context.Context, task recognition.Checkpoint) error {
	return s.Transaction(ctx, "receipt:"+task.Key, func(raw json.RawMessage) (any, error) {
		var previous recognition.Checkpoint
		if err := json.Unmarshal(raw, &previous); err != nil {
			return nil, err
		}
		if previous.Scope != task.Scope || previous.Key != task.Key {
			return nil, fmt.Errorf("receipt state identity mismatch")
		}
		if previous.Delivered {
			return nil, nil
		}
		return task, nil
	})
}
func (s *Files) PendingRecognition(ctx context.Context, scope string) ([]recognition.Checkpoint, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var tasks []recognition.Checkpoint
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
		var task recognition.Checkpoint
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, err
		}
		if task.Scope == scope && !task.Delivered {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}
func (s *Files) RecognitionBaseline(ctx context.Context, scope string, initial map[string]bool) (map[string]bool, bool, error) {
	var baseline map[string]bool
	found := false
	err := s.Transaction(ctx, "baseline:"+scope, func(raw json.RawMessage) (any, error) {
		if len(raw) > 0 {
			found = true
			return nil, json.Unmarshal(raw, &baseline)
		}
		if initial == nil {
			return nil, nil
		}
		baseline = initial
		return initial, nil
	})
	return baseline, found, err
}
