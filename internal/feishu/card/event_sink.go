package card

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
)

// EventSinkOptions configures real-time transaction event handling.
type EventSinkOptions struct {
	Debounce time.Duration // Debounce duration before notifying, e.g. 3-5 seconds. Default 3s.
	Logger   *slog.Logger
}

// EventSink listens to Feishu Bitable change events on the transactions table
// and triggers sub-second/real-time notification after a brief debounce.
type EventSink struct {
	svc      *Service
	debounce time.Duration
	logger   *slog.Logger
	mu       sync.Mutex
	timers   map[string]*time.Timer
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewEventSink instantiates a real-time event sink for the transactions table.
func NewEventSink(svc *Service, opts ...EventSinkOptions) *EventSink {
	opt := EventSinkOptions{Debounce: 3 * time.Second}
	if len(opts) > 0 {
		if opts[0].Debounce > 0 {
			opt.Debounce = opts[0].Debounce
		}
		if opts[0].Logger != nil {
			opt.Logger = opts[0].Logger
		}
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &EventSink{
		svc:      svc,
		debounce: opt.Debounce,
		logger:   opt.Logger,
		timers:   make(map[string]*time.Timer),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Close cancels all pending debounced timers.
func (s *EventSink) Close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.timers {
		t.Stop()
	}
	s.timers = make(map[string]*time.Timer)
}

// Sink handles incoming Bitable record changed events from Feishu long connection.
func (s *EventSink) Sink(ctx context.Context, incoming events.Event) error {
	transBinding := s.svc.cfg.Business.Tables.Transactions
	if transBinding.BaseToken == "" || transBinding.TableID == "" {
		return nil
	}

	var body struct {
		Event struct {
			FileToken  string `json:"file_token"`
			TableID    string `json:"table_id"`
			ActionList []struct {
				Action   string `json:"action"`
				RecordID string `json:"record_id"`
			} `json:"action_list"`
		} `json:"event"`
	}

	if err := json.Unmarshal(incoming.Payload, &body); err != nil {
		return fmt.Errorf("decode bitable event for transactions: %w", err)
	}

	// Only process events targeting our configured transactions table
	if body.Event.FileToken != transBinding.BaseToken || body.Event.TableID != transBinding.TableID {
		return nil
	}

	for _, action := range body.Event.ActionList {
		if action.Action != "record_added" && action.Action != "record_edited" {
			continue
		}
		recordID := action.RecordID
		if recordID == "" {
			continue
		}

		s.enqueueDebounced(recordID)
	}

	return nil
}

func (s *EventSink) enqueueDebounced(recordID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If timer already running for this record, reset it (debouncing)
	if t, exists := s.timers[recordID]; exists {
		t.Stop()
	}

	s.timers[recordID] = time.AfterFunc(s.debounce, func() {
		s.mu.Lock()
		delete(s.timers, recordID)
		s.mu.Unlock()

		if s.ctx.Err() != nil {
			return
		}

		s.logger.InfoContext(s.ctx, "processing real-time transaction event", "record_id", recordID)
		res, err := s.svc.NotifyTransaction(s.ctx, recordID)
		if err != nil {
			s.logger.WarnContext(s.ctx, "real-time notify transaction failed", "record_id", recordID, "error", err)
			return
		}

		if res != nil {
			if res.Status == "success" {
				s.logger.InfoContext(s.ctx, "real-time transaction notification sent successfully",
					"record_id", recordID,
					"recipient", res.Recipient,
					"tx_id", res.TransactionID,
					"merchant", res.Merchant,
					"amount", res.BookedAmount,
				)
			} else {
				s.logger.DebugContext(s.ctx, "real-time transaction notification skipped",
					"record_id", recordID,
					"status", res.Status,
					"reason", res.Reason,
				)
			}
		}
	})
}
