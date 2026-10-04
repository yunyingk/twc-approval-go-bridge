package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
)

type ReviewSourceBinding struct {
	Kind                 app.SourceKind
	BaseToken, TableID   string
	FieldIDs             []string
	InvoiceNumberFieldID string
}

type ReviewSourceChangeConsumer interface {
	EnqueueSourceChange(context.Context, app.SourceChange) error
}

type ReviewSourceChangeSink struct {
	scope    string
	bindings []ReviewSourceBinding
	consumer ReviewSourceChangeConsumer
}

func NewReviewSourceChangeSink(scope string, bindings []ReviewSourceBinding, consumer ReviewSourceChangeConsumer) (*ReviewSourceChangeSink, error) {
	if scope == "" || len(bindings) == 0 || consumer == nil {
		return nil, fmt.Errorf("review source events require scope, bindings and consumer")
	}
	copied := make([]ReviewSourceBinding, 0, len(bindings))
	seen := make(map[string]bool)
	for _, binding := range bindings {
		key := binding.BaseToken + ":" + binding.TableID
		if binding.BaseToken == "" || binding.TableID == "" || len(binding.FieldIDs) == 0 || seen[key] ||
			(binding.Kind != app.TransactionSource && binding.Kind != app.InvoiceSource) {
			return nil, fmt.Errorf("invalid review event source binding")
		}
		if binding.Kind == app.InvoiceSource && binding.InvoiceNumberFieldID == "" {
			return nil, fmt.Errorf("invoice change events require invoice number field")
		}
		binding.FieldIDs = append([]string(nil), binding.FieldIDs...)
		copied = append(copied, binding)
		seen[key] = true
	}
	return &ReviewSourceChangeSink{scope: scope, bindings: copied, consumer: consumer}, nil
}

func (s *ReviewSourceChangeSink) Sink(ctx context.Context, incoming Event) error {
	var body struct {
		Event struct {
			FileToken string `json:"file_token"`
			TableID   string `json:"table_id"`
			Actions   []struct {
				Action   string       `json:"action"`
				RecordID string       `json:"record_id"`
				Before   []fieldValue `json:"before_value"`
				After    []fieldValue `json:"after_value"`
			} `json:"action_list"`
		} `json:"event"`
	}
	if err := json.Unmarshal(incoming.Payload, &body); err != nil {
		return fmt.Errorf("decode review source event: %w", err)
	}
	for _, binding := range s.bindings {
		if body.Event.FileToken != binding.BaseToken || body.Event.TableID != binding.TableID {
			continue
		}
		watched := make(map[string]bool)
		for _, id := range binding.FieldIDs {
			if id != "" {
				watched[id] = true
			}
		}
		changes := make(map[string]app.SourceChange)
		for _, action := range body.Event.Actions {
			if action.RecordID == "" {
				continue
			}
			changed := action.Action == "record_added" || action.Action == "record_deleted"
			if action.Action == "record_edited" {
				for _, field := range append(action.Before, action.After...) {
					if watched[field.FieldID] && fieldText(action.Before, field.FieldID) != fieldText(action.After, field.FieldID) {
						changed = true
						break
					}
				}
			}
			if !changed {
				continue
			}
			change, exists := changes[action.RecordID]
			if !exists {
				change = app.SourceChange{Scope: s.scope, Source: "feishu:" + binding.BaseToken + ":" + binding.TableID, Kind: binding.Kind, RecordID: action.RecordID, EventID: incoming.ID}
			}
			if binding.Kind == app.InvoiceSource {
				mentioned := false
				for _, field := range append(action.Before, action.After...) {
					if field.FieldID != binding.InvoiceNumberFieldID {
						continue
					}
					mentioned = true
					value, ok := eventText(field.Value)
					if !ok {
						change.AllDetails = true
					} else if value != "" {
						change.InvoiceNumbers = append(change.InvoiceNumbers, value)
					}
				}
				// New/deleted rows can change a candidate population that was not
				// present in older snapshots. Missing hints widen the read scope.
				if !mentioned && (action.Action == "record_added" || action.Action == "record_deleted") {
					change.AllDetails = true
				}
			}
			changes[action.RecordID] = change
		}
		ids := make([]string, 0, len(changes))
		for id := range changes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if err := s.consumer.EnqueueSourceChange(ctx, changes[id]); err != nil {
				return err
			}
		}
	}
	return nil
}

// Feishu event field values are serialized JSON, independently of REST field
// display names. Unsupported forms invalidate broadly rather than invent IDs.
func eventText(value string) (string, bool) {
	if value == "" || strings.TrimSpace(value) == "null" {
		return "", true
	}
	var text string
	if err := json.Unmarshal([]byte(value), &text); err == nil {
		return text, true
	}
	var segments []struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal([]byte(value), &segments); err != nil {
		return "", false
	}
	var result strings.Builder
	for _, segment := range segments {
		if segment.Text == nil {
			return "", false
		}
		result.WriteString(*segment.Text)
	}
	return result.String(), true
}
