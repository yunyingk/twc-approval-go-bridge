package recognition

import (
	"context"
	"log/slog"
	"time"
)

// RecordScanner yields attachment tokens from the configured Base and table.
type RecordScanner interface {
	ScanAttachmentRecords(context.Context, string, string, string, func(string, map[string]bool) error) error
}

// Poll records an in-memory baseline immediately, then enqueues only newly seen
// attachments on later scans. processExisting also enqueues the first scan.
func (p *Processor) Poll(ctx context.Context, scanner RecordScanner, interval time.Duration, processExisting bool) {
	if scanner == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	baseline := make(map[string]bool)
	baselineReady := processExisting
	if p.checkpoints != nil && !processExisting {
		saved, found, err := p.checkpoints.RecognitionBaseline(ctx, p.scope, nil)
		if err != nil {
			p.logger.ErrorContext(ctx, "load receipt baseline", "error", err)
			return
		}
		if found {
			baseline, baselineReady = saved, true
		}
	}
	for {
		started := time.Now()
		count := 0
		current := make(map[string]bool)
		err := scanner.ScanAttachmentRecords(ctx, p.config.BaseToken, p.config.TableID, p.config.FieldID, func(recordID string, tokens map[string]bool) error {
			count++
			newTokens := make(map[string]bool)
			for token := range tokens {
				key := recordID + ":" + token
				current[key] = true
				if baselineReady && !baseline[key] {
					newTokens[token] = true
				}
			}
			if len(newTokens) > 0 {
				return p.EnqueueRecord(ctx, recordID, newTokens)
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			p.logger.ErrorContext(ctx, "receipt poll failed", "error", err)
		} else if err == nil {
			if !baselineReady {
				if p.checkpoints != nil {
					saved, _, saveErr := p.checkpoints.RecognitionBaseline(ctx, p.scope, current)
					if saveErr != nil {
						p.logger.ErrorContext(ctx, "save receipt baseline", "error", saveErr)
					} else {
						baseline, baselineReady = saved, true
					}
				} else {
					baseline, baselineReady = current, true
				}
			}
			p.logger.LogAttrs(ctx, slog.LevelInfo, "receipt poll complete", slog.Int("records_with_attachments", count), slog.Duration("duration", time.Since(started)))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
