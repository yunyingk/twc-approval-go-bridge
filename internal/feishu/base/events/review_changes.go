package events

import (
	"context"
	"encoding/json"
	"fmt"
)

type ReviewChangeConsumer interface {
	NotifyChange(context.Context, string, string) error
}

// ReviewChangeSink watches only effective inputs of a detail snapshot. AI
// writeback and workflow fields must not create a feedback loop.
type ReviewChangeSink struct {
	base, table string
	fields      map[string]bool
	consumer    ReviewChangeConsumer
}

func NewReviewChangeSink(base, table string, fieldIDs []string, consumer ReviewChangeConsumer) (*ReviewChangeSink, error) {
	if base == "" || table == "" || consumer == nil {
		return nil, fmt.Errorf("review changes require source Base, table and consumer")
	}
	fields := make(map[string]bool)
	for _, id := range fieldIDs {
		if id != "" {
			fields[id] = true
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("review changes require effective input fields")
	}
	return &ReviewChangeSink{base: base, table: table, fields: fields, consumer: consumer}, nil
}

// Sink persists intent before WebSocket acknowledgement; source loading and
// provider submission happen in the shared background worker.
func (s *ReviewChangeSink) Sink(ctx context.Context, incoming Event) error {
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
		return fmt.Errorf("decode review change event: %w", err)
	}
	if body.Event.FileToken != s.base || body.Event.TableID != s.table {
		return nil
	}
	for _, action := range body.Event.Actions {
		if action.RecordID == "" {
			continue
		}
		changed := action.Action == "record_deleted"
		if action.Action == "record_edited" || action.Action == "record_added" {
			// Only explicitly mentioned fields were changed. Missing fields in
			// an event are not evidence that other values were cleared.
			for _, field := range append(action.Before, action.After...) {
				if s.fields[field.FieldID] && fieldText(action.Before, field.FieldID) != fieldText(action.After, field.FieldID) {
					changed = true
					break
				}
			}
		}
		if changed {
			if err := s.consumer.NotifyChange(ctx, action.RecordID, incoming.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
