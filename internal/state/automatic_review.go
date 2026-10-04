package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
)

func automaticReviewKey(scope, recordID string) string {
	return "automatic-review:" + scope + ":" + recordID
}

func (s *Files) QueueAutomaticReview(ctx context.Context, scope, recordID string) error {
	if scope == "" || recordID == "" {
		return fmt.Errorf("automatic review scope and record are required")
	}
	return s.Transaction(ctx, automaticReviewKey(scope, recordID), func(raw json.RawMessage) (any, error) {
		intent := app.AutomaticIntent{SourceScope: scope, RecordID: recordID}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &intent); err != nil {
				return nil, err
			}
			if intent.SourceScope != scope || intent.RecordID != recordID {
				return nil, fmt.Errorf("automatic review state identity mismatch")
			}
		}
		intent.Generation++
		intent.Pending = true
		return intent, nil
	})
}

func (s *Files) PendingAutomaticReviews(ctx context.Context, scope string) ([]app.AutomaticIntent, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var intents []app.AutomaticIntent
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
		var intent app.AutomaticIntent
		if err := json.Unmarshal(raw, &intent); err != nil {
			return nil, err
		}
		if intent.SourceScope == scope && intent.Pending {
			intents = append(intents, intent)
		}
	}
	return intents, nil
}

func (s *Files) AcknowledgeAutomaticReview(ctx context.Context, processed app.AutomaticIntent) error {
	return s.Transaction(ctx, automaticReviewKey(processed.SourceScope, processed.RecordID), func(raw json.RawMessage) (any, error) {
		var current app.AutomaticIntent
		if len(raw) == 0 {
			return nil, fmt.Errorf("unknown automatic review intent")
		}
		if err := json.Unmarshal(raw, &current); err != nil {
			return nil, err
		}
		if current.SourceScope != processed.SourceScope || current.RecordID != processed.RecordID {
			return nil, fmt.Errorf("automatic review state identity mismatch")
		}
		if current.Generation != processed.Generation {
			return nil, nil
		}
		current.Pending = false
		return current, nil
	})
}
