package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

func sourceChangeKey(change app.SourceChange) string {
	return "review-source-change:" + change.Scope + ":" + string(change.Kind) + ":" + change.Source + ":" + change.RecordID + ":" + change.EventID
}

func (s *Files) QueueReviewSourceChange(ctx context.Context, change app.SourceChange) error {
	if err := change.Validate(); err != nil {
		return err
	}
	change.InvoiceNumbers = append([]string(nil), change.InvoiceNumbers...)
	sort.Strings(change.InvoiceNumbers)
	change.InvoiceNumbers = compactStrings(change.InvoiceNumbers)
	return s.Transaction(ctx, sourceChangeKey(change), func(raw json.RawMessage) (any, error) {
		if len(raw) > 0 {
			var saved app.SourceChange
			if err := json.Unmarshal(raw, &saved); err != nil {
				return nil, err
			}
			incoming := change
			incoming.ReceivedAt, incoming.Pending = saved.ReceivedAt, saved.Pending
			if !reflect.DeepEqual(saved, incoming) {
				return nil, fmt.Errorf("conflicting review source event")
			}
			return nil, nil
		}
		change.ReceivedAt = time.Now().UTC()
		change.Pending = true
		return change, nil
	})
}

func compactStrings(values []string) []string {
	var result []string
	for _, value := range values {
		if value != "" && (len(result) == 0 || result[len(result)-1] != value) {
			result = append(result, value)
		}
	}
	return result
}

func (s *Files) PendingReviewSourceChanges(ctx context.Context, scope string) ([]app.SourceChange, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var changes []app.SourceChange
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
		var change app.SourceChange
		if err := json.Unmarshal(raw, &change); err != nil {
			return nil, err
		}
		if change.Scope == scope && change.Kind != "" && change.Pending {
			changes = append(changes, change)
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ReceivedAt.Before(changes[j].ReceivedAt) })
	return changes, nil
}

func (s *Files) AcknowledgeReviewSourceChange(ctx context.Context, processed app.SourceChange) error {
	return s.Transaction(ctx, sourceChangeKey(processed), func(raw json.RawMessage) (any, error) {
		var saved app.SourceChange
		if len(raw) == 0 {
			return nil, fmt.Errorf("unknown review source change")
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, err
		}
		if sourceChangeKey(saved) != sourceChangeKey(processed) {
			return nil, fmt.Errorf("review source change identity mismatch")
		}
		saved.Pending = false
		return saved, nil
	})
}

// ReviewAttempts returns only this logical source's durable snapshots. Other
// state records and previously configured tenants remain isolated.
func (s *Files) ReviewAttempts(ctx context.Context, scope string) ([]core.Attempt, error) {
	if scope == "" {
		return nil, fmt.Errorf("review attempt scope is required")
	}
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
		id := attempt.Request.Document.RecordID
		if id != "" && attempt.Request.LogicalID == scope+":"+id {
			attempts = append(attempts, attempt)
		}
	}
	return attempts, nil
}
