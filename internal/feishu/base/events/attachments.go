package events

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	"log/slog"
	"strings"
)

type ChangeConsumer interface {
	EnqueueChange(context.Context, recognition.Change) error
}
type AttachmentSink struct {
	config   recognition.Config
	consumer ChangeConsumer
}

func NewAttachmentSink(config recognition.Config, consumer ChangeConsumer) *AttachmentSink {
	return &AttachmentSink{config: config, consumer: consumer}
}

// Sink only enqueues matching attachment changes so the Feishu event can be acknowledged quickly.
func (p *AttachmentSink) Sink(ctx context.Context, incoming Event) error {
	var body struct {
		Event struct {
			FileToken  string `json:"file_token"`
			TableID    string `json:"table_id"`
			ActionList []struct {
				Action   string       `json:"action"`
				RecordID string       `json:"record_id"`
				Before   []fieldValue `json:"before_value"`
				After    []fieldValue `json:"after_value"`
			} `json:"action_list"`
		} `json:"event"`
	}
	if err := json.Unmarshal(incoming.Payload, &body); err != nil {
		return fmt.Errorf("decode Bitable event: %w", err)
	}
	if body.Event.FileToken != p.config.BaseToken || body.Event.TableID != p.config.TableID {
		return nil
	}
	for _, action := range body.Event.ActionList {
		if action.Action != "record_added" && action.Action != "record_edited" {
			continue
		}
		if action.RecordID == "" {
			continue
		}
		before, after := fieldText(action.Before, p.config.FieldID), fieldText(action.After, p.config.FieldID)
		if after == "" || after == "[]" || after == "null" || after == before {
			continue
		}
		current, ok := attachmentTokens(after)
		if !ok {
			slog.Default().WarnContext(ctx, "skip unrecognized attachment event value", "record_id", action.RecordID)
			continue
		}
		previous, _ := attachmentTokens(before)
		newTokens := make(map[string]bool)
		for token := range current {
			if !previous[token] {
				newTokens[token] = true
			}
		}
		if len(newTokens) == 0 {
			continue
		}
		if err := p.consumer.EnqueueChange(ctx, recognition.Change{BaseToken: p.config.BaseToken, TableID: p.config.TableID, FieldID: p.config.FieldID, RecordID: action.RecordID, EventID: incoming.ID, Tokens: newTokens}); err != nil {
			return err
		}
	}
	return nil
}

type fieldValue struct {
	FieldID string `json:"field_id"`
	Value   string `json:"field_value"`
}

func fieldText(fields []fieldValue, id string) string {
	for _, field := range fields {
		if field.FieldID == id {
			return strings.TrimSpace(field.Value)
		}
	}
	return ""
}

func attachmentTokens(value string) (map[string]bool, bool) {
	var files []struct {
		FileToken       string `json:"file_token"`
		Token           string `json:"token"`
		AttachmentToken string `json:"attachmentToken"`
	}
	if err := json.Unmarshal([]byte(value), &files); err != nil {
		return nil, false
	}
	tokens := make(map[string]bool)
	for _, file := range files {
		token := file.FileToken
		if token == "" {
			token = file.Token
		}
		if token == "" {
			token = file.AttachmentToken
		}
		if token == "" {
			return nil, false
		}
		tokens[token] = true
	}
	return tokens, true
}
