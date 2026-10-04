package recognition

import (
	"context"
	"fmt"
	"time"
)

type Checkpoint struct {
	Key, Scope            string
	Result                Result
	Recognized, Delivered bool
}
type Checkpoints interface {
	QueueRecognition(context.Context, Checkpoint) error
	LoadRecognition(context.Context, string) (Checkpoint, error)
	SaveRecognition(context.Context, Checkpoint) error
	PendingRecognition(context.Context, string) ([]Checkpoint, error)
	RecognitionBaseline(context.Context, string, map[string]bool) (map[string]bool, bool, error)
}

// WithCheckpoints is called during assembly, before any goroutine is started.
func (p *Processor) WithCheckpoints(store Checkpoints, scope string) *Processor {
	p.checkpoints, p.scope = store, scope
	return p
}
func (p *Processor) checkpointKey(recordID, token string) string {
	return p.scope + ":" + recordID + ":" + token
}
func (p *Processor) queue(ctx context.Context, j job) error {
	if p.checkpoints == nil {
		return nil
	}
	for token := range j.tokens {
		result := Result{BaseToken: p.config.BaseToken, TableID: p.config.TableID, RecordID: j.recordID, FileToken: token, EventID: j.eventID, Trigger: j.trigger}
		if err := p.checkpoints.QueueRecognition(ctx, Checkpoint{Key: p.checkpointKey(j.recordID, token), Scope: p.scope, Result: result}); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) recover(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		pending, err := p.checkpoints.PendingRecognition(ctx, p.scope)
		if err != nil {
			p.logger.ErrorContext(ctx, "load pending receipt tasks", "error", err)
		}
		for _, task := range pending {
			if task.Result.BaseToken != p.config.BaseToken || task.Result.TableID != p.config.TableID {
				p.logger.ErrorContext(ctx, "receipt state source mismatch")
				continue
			}
			j := job{eventID: task.Result.EventID, trigger: "recovery", recordID: task.Result.RecordID, tokens: map[string]bool{task.Result.FileToken: true}}
			select {
			case p.jobs <- j:
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Processor) loadCheckpoint(ctx context.Context, recordID, token string) (Checkpoint, error) {
	checkpoint, err := p.checkpoints.LoadRecognition(ctx, p.checkpointKey(recordID, token))
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.Scope != p.scope || checkpoint.Result.RecordID != recordID || checkpoint.Result.FileToken != token {
		return Checkpoint{}, fmt.Errorf("receipt checkpoint identity mismatch")
	}
	return checkpoint, nil
}
