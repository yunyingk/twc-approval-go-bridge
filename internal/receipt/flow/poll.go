package flow

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
				baseline = current
				baselineReady = true
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
