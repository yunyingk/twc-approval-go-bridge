// Package flow connects Feishu record changes and polling to receipt recognition.
package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
)

type Config struct {
	// BaseToken, TableID and FieldID identify the employee-filled attachment cell.
	// They must not point at the third-party-managed transaction table.
	BaseToken string
	TableID   string
	FieldID   string
}

type Result struct {
	EventID     string
	Trigger     string
	BaseToken   string
	TableID     string
	RecordID    string
	FileToken   string
	FileName    string
	Recognition invoice.Recognition
}

type ResultHandler func(context.Context, Result) error

type AttachmentReader interface {
	ReadAttachments(context.Context, string, string, string, string, map[string]bool) ([]invoice.Attachment, []string, error)
}

type job struct {
	eventID  string
	trigger  string
	recordID string
	tokens   map[string]bool
}

type Processor struct {
	config     Config
	reader     AttachmentReader
	recognizer invoice.Recognizer
	handler    ResultHandler
	logger     *slog.Logger
	jobs       chan job
}

func New(config Config, reader AttachmentReader, recognizer invoice.Recognizer, handler ResultHandler, logger *slog.Logger) (*Processor, error) {
	if config.BaseToken == "" || config.TableID == "" || config.FieldID == "" || reader == nil || recognizer == nil || handler == nil {
		return nil, fmt.Errorf("receipt flow requires Base, Table, field, reader, recognizer and result handler")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{config: config, reader: reader, recognizer: recognizer, handler: handler, logger: logger, jobs: make(chan job, 64)}, nil
}

// Sink only enqueues matching attachment changes so the Feishu event can be acknowledged quickly.
func (p *Processor) Sink(ctx context.Context, incoming events.Event) error {
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
			p.logger.WarnContext(ctx, "skip unrecognized attachment event value", "record_id", action.RecordID)
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
		select {
		case p.jobs <- job{eventID: incoming.ID, trigger: "event", recordID: action.RecordID, tokens: newTokens}:
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("receipt event queue is full")
		}
	}
	return nil
}

// EnqueueRecord sends a polled record through the same recognition and deduplication path as events.
// Polling waits for queue capacity; event delivery must remain fast and uses Sink instead.
func (p *Processor) EnqueueRecord(ctx context.Context, recordID string, tokens map[string]bool) error {
	if recordID == "" || len(tokens) == 0 {
		return nil
	}
	select {
	case p.jobs <- job{trigger: "poll", recordID: recordID, tokens: tokens}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
		FileToken string `json:"file_token"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal([]byte(value), &files); err != nil {
		return nil, false
	}
	tokens := make(map[string]bool)
	for _, file := range files {
		if file.FileToken != "" {
			tokens[file.FileToken] = true
		} else if file.Token != "" {
			tokens[file.Token] = true
		}
	}
	return tokens, true
}

func (p *Processor) Run(ctx context.Context) {
	seen := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.jobs:
			pending := make(map[string]bool, len(job.tokens))
			for token := range job.tokens {
				if !seen[job.recordID+":"+token] {
					pending[token] = true
				}
			}
			if len(pending) == 0 {
				continue
			}
			workCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			var attachments []invoice.Attachment
			var tokens []string
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				attachments, tokens, err = p.reader.ReadAttachments(workCtx, p.config.BaseToken, p.config.TableID, job.recordID, p.config.FieldID, pending)
				if err != nil || len(attachments) > 0 {
					break
				}
				select {
				case <-workCtx.Done():
					err = workCtx.Err()
				case <-time.After(500 * time.Millisecond):
				}
				if err != nil {
					break
				}
			}
			if err == nil {
				for i, attachment := range attachments {
					if !pending[tokens[i]] {
						continue
					}
					key := job.recordID + ":" + tokens[i]
					if seen[key] {
						continue
					}
					result, recognizeErr := p.recognizer.Recognize(workCtx, attachment)
					if recognizeErr != nil {
						err = recognizeErr
						break
					}
					if handleErr := p.handler(workCtx, Result{EventID: job.eventID, Trigger: job.trigger, BaseToken: p.config.BaseToken, TableID: p.config.TableID, RecordID: job.recordID, FileToken: tokens[i], FileName: attachment.Name, Recognition: result}); handleErr != nil {
						err = handleErr
						break
					}
					seen[key] = true
				}
			}
			cancel()
			if err != nil {
				p.logger.Error("receipt processing failed", "record_id", job.recordID, "error", err)
			}
		}
	}
}
