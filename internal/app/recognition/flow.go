// Package recognition coordinates attachment recognition independently of its providers.
package recognition

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/audit"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
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
	config      Config
	reader      AttachmentReader
	recognizer  invoice.Recognizer
	handler     ResultHandler
	logger      *slog.Logger
	checkpoints Checkpoints
	scope       string
	jobs        chan job
	notifier    DeadLetterNotifier
	feishuHost  string
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

// Change describes a source attachment change after transport decoding.
type Change struct {
	BaseToken, TableID, FieldID, RecordID, EventID string
	Tokens                                         map[string]bool
}

// EnqueueChange acknowledges matching changes quickly; polling may wait for capacity.
func (p *Processor) EnqueueChange(ctx context.Context, change Change) error {
	if change.BaseToken != p.config.BaseToken || change.TableID != p.config.TableID || change.FieldID != p.config.FieldID || change.RecordID == "" || len(change.Tokens) == 0 {
		return nil
	}
	j := job{eventID: change.EventID, trigger: "event", recordID: change.RecordID, tokens: change.Tokens}
	if err := p.queue(ctx, j); err != nil {
		return err
	}
	select {
	case p.jobs <- j:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("receipt event queue is full")
	}
}

// EnqueueRecord sends a polled record through the same recognition and deduplication path as events.
// Polling waits for queue capacity; event delivery must remain fast and uses Sink instead.
func (p *Processor) EnqueueRecord(ctx context.Context, recordID string, tokens map[string]bool) error {
	if recordID == "" || len(tokens) == 0 {
		return nil
	}
	j := job{trigger: "poll", recordID: recordID, tokens: tokens}
	if err := p.queue(ctx, j); err != nil {
		return err
	}
	select {
	case p.jobs <- j:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Processor) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if p.checkpoints != nil {
		go p.recover(ctx)
	}
	seen := make(map[string]bool)
	recognized := make(map[string]invoice.Recognition)
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
			if err == nil && len(attachments) != len(tokens) {
				err = fmt.Errorf("attachment reader returned mismatched files and tokens")
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
					var checkpoint Checkpoint
					if p.checkpoints != nil {
						checkpoint, err = p.loadCheckpoint(workCtx, job.recordID, tokens[i])
						if err != nil {
							break
						}
						if checkpoint.Delivered || checkpoint.Failed {
							seen[key] = true
							continue
						}
						if checkpoint.Recognized {
							recognized[key] = checkpoint.Result.Recognition
						}
					}
					result, cached := recognized[key]
					var recognizeErr error
					if !cached {
						if p.checkpoints != nil {
							checkpoint.Attempts++
							checkpoint.LastAttemptAt = time.Now()
						}
						ocrStart := time.Now()
						audit.Log(audit.Entry{
							Timestamp: ocrStart,
							Type:      audit.TypeOCRInitiated,
							RecordID:  job.recordID,
							Action:    fmt.Sprintf("发起小票 OCR 识别 (文件: %s, 第 %d 次)", attachment.Name, checkpoint.Attempts),
							Details: map[string]any{
								"file_token": tokens[i],
								"file_name":  attachment.Name,
								"attempts":   checkpoint.Attempts,
								"trigger":    job.trigger,
								"event_id":   job.eventID,
							},
						})
						result, recognizeErr = p.recognizer.Recognize(workCtx, attachment)
						if recognizeErr == nil {
							recognized[key] = result
							amount := result.Total
							merchant := result.Title
							currency := ""
							if result.Facts != nil {
								if result.Facts.Total != "" {
									amount = result.Facts.Total
								}
								if result.Facts.Seller != "" {
									merchant = result.Facts.Seller
								}
								currency = result.Facts.Currency
							}
							audit.Log(audit.Entry{
								Timestamp: time.Now(),
								Type:      audit.TypeOCRCompleted,
								RecordID:  job.recordID,
								Action:    fmt.Sprintf("小票 OCR 识别完成 (金额: %s %s, 商户: %s)", currency, amount, merchant),
								Details: map[string]any{
									"file_token":  tokens[i],
									"file_name":   attachment.Name,
									"amount":      amount,
									"currency":    currency,
									"merchant":    merchant,
									"date":        result.Date,
									"trace_id":    result.Origin.TraceID,
									"duration_ms": time.Since(ocrStart).Milliseconds(),
								},
							})
						} else {
							audit.Log(audit.Entry{
								Timestamp: time.Now(),
								Type:      audit.TypeOCRFailed,
								RecordID:  job.recordID,
								Action:    fmt.Sprintf("小票 OCR 识别失败: %v", recognizeErr),
								Details: map[string]any{
									"file_token": tokens[i],
									"file_name":  attachment.Name,
									"attempts":   checkpoint.Attempts,
									"error":      recognizeErr.Error(),
								},
							})
							if p.checkpoints != nil {
								checkpoint.LastError = recognizeErr.Error()
								if checkpoint.Attempts >= DefaultMaxRecognitionAttempts {
									checkpoint.Failed = true
									p.logger.ErrorContext(workCtx, "receipt recognition exceeded max attempts, marked as dead letter to stop retries",
										"record_id", job.recordID, "file_token", tokens[i], "file_name", attachment.Name,
										"attempts", checkpoint.Attempts, "error", recognizeErr)
									if p.notifier != nil {
										var recordURL string
										if p.feishuHost != "" && p.config.BaseToken != "" && p.config.TableID != "" {
											recordURL = fmt.Sprintf("%s/base/%s?table=%s&record=%s", strings.TrimRight(p.feishuHost, "/"), p.config.BaseToken, p.config.TableID, job.recordID)
										}
										notice := DeadLetterNotice{
											RecordID:  job.recordID,
											RecordURL: recordURL,
											FileName:  attachment.Name,
											FileToken: tokens[i],
											Attempts:  checkpoint.Attempts,
											LastError: recognizeErr.Error(),
											FailedAt:  time.Now(),
										}
										go func(n DeadLetterNotice) {
											notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
											defer cancel()
											if notifyErr := p.notifier.NotifyDeadLetter(notifyCtx, n); notifyErr != nil {
												p.logger.WarnContext(notifyCtx, "send dead letter notification failed", "record_id", n.RecordID, "error", notifyErr)
											}
										}(notice)
									}
								}
								_ = p.checkpoints.SaveRecognition(workCtx, checkpoint)
							}
						}
					}
					if recognizeErr != nil {
						err = recognizeErr
						break
					}
					output := Result{EventID: job.eventID, Trigger: job.trigger, BaseToken: p.config.BaseToken, TableID: p.config.TableID, RecordID: job.recordID, FileToken: tokens[i], FileName: attachment.Name, Recognition: result}
					if p.checkpoints != nil {
						checkpoint.Result, checkpoint.Recognized = output, true
						if err = p.checkpoints.SaveRecognition(workCtx, checkpoint); err != nil {
							break
						}
					}
					if handleErr := p.handler(workCtx, output); handleErr != nil {
						err = handleErr
						if p.checkpoints != nil {
							checkpoint.LastError = handleErr.Error()
							if checkpoint.Attempts >= DefaultMaxRecognitionAttempts {
								checkpoint.Failed = true
								p.logger.ErrorContext(workCtx, "receipt delivery exceeded max attempts, marked as dead letter",
									"record_id", job.recordID, "file_token", tokens[i], "attempts", checkpoint.Attempts, "error", handleErr)
							}
							_ = p.checkpoints.SaveRecognition(workCtx, checkpoint)
						}
						break
					}
					if p.checkpoints != nil {
						checkpoint.Delivered = true
						if err = p.checkpoints.SaveRecognition(workCtx, checkpoint); err != nil {
							break
						}
					}
					seen[key] = true
					delete(recognized, key)
				}
			}
			cancel()
			if err != nil {
				p.logger.Error("receipt processing failed", "record_id", job.recordID, "error", err)
			}
		}
	}
}
