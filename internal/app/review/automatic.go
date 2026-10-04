package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// AutomaticIntent survives a restart between ledger delivery and submission.
// Generation prevents a receipt arriving during submission from being discarded.
type AutomaticIntent struct {
	SourceScope    string    `json:"source_scope"`
	RecordID       string    `json:"record_id"`
	Generation     uint64    `json:"generation"`
	Pending        bool      `json:"pending"`
	NotBefore      time.Time `json:"not_before,omitempty"`
	ChangeEventIDs []string  `json:"change_event_ids,omitempty"`
}

// ChangeStore coalesces edit notifications durably, including event redelivery.
// It does no source reads and never calls an audit provider.
type ChangeStore interface {
	QueueAutomaticReviewChange(context.Context, string, string, string, time.Time) error
}

type AutomaticStore interface {
	QueueAutomaticReview(context.Context, string, string) error
	PendingAutomaticReviews(context.Context, string) ([]AutomaticIntent, error)
	AcknowledgeAutomaticReview(context.Context, AutomaticIntent) error
}

type Automatic struct {
	service        *Service
	store          AutomaticStore
	scope          string
	logger         *slog.Logger
	wake           chan struct{}
	changeDebounce time.Duration
}

func (a *Automatic) EnableChanges(debounce time.Duration) error {
	if _, ok := a.store.(ChangeStore); !ok || debounce <= 0 {
		return fmt.Errorf("review changes require a persistent change store and positive debounce")
	}
	a.changeDebounce = debounce
	return nil
}

func (a *Automatic) NotifyChange(ctx context.Context, recordID, eventID string) error {
	if a.changeDebounce <= 0 || strings.TrimSpace(recordID) == "" || strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("review change requires enabled changes, record ID and event ID")
	}
	store, ok := a.store.(ChangeStore)
	if !ok {
		return fmt.Errorf("review change store is unavailable")
	}
	deadline := time.Now().Add(a.changeDebounce)
	if err := store.QueueAutomaticReviewChange(ctx, a.scope, recordID, eventID, deadline); err != nil {
		return err
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
	a.logger.InfoContext(ctx, "automatic review change queued", "record_id", recordID, "event_id", eventID, "source_scope", a.scope)
	return nil
}

func NewAutomatic(service *Service, store AutomaticStore, scope string, logger *slog.Logger) (*Automatic, error) {
	if service == nil || store == nil || strings.TrimSpace(scope) == "" || service.options.Store == nil {
		return nil, fmt.Errorf("automatic review requires a service, persistent attempt and trigger stores, and source scope")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Automatic{service: service, store: store, scope: scope, logger: logger, wake: make(chan struct{}, 1)}, nil
}

// NotifyRecognition is called only after successful invoice-ledger delivery.
// It saves intent before acknowledging receipt delivery and never calls a provider.
func (a *Automatic) NotifyRecognition(ctx context.Context, recordID string) error {
	if strings.TrimSpace(recordID) == "" {
		return fmt.Errorf("automatic review source record is required")
	}
	if err := a.store.QueueAutomaticReview(ctx, a.scope, recordID); err != nil {
		return err
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
	a.logger.InfoContext(ctx, "automatic review queued", "record_id", recordID, "source_scope", a.scope)
	return nil
}

// ProcessPending uses Submit's full-attachment validation and durable version
// dedupe. Incomplete ledgers and uncertain submissions remain pending, while
// Submit refuses to repeat an uncertain provider request for the same version.
func (a *Automatic) ProcessPending(ctx context.Context) error {
	intents, err := a.store.PendingAutomaticReviews(ctx, a.scope)
	if err != nil {
		return err
	}
	var failures []error
	for _, intent := range intents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if intent.SourceScope != a.scope || intent.RecordID == "" {
			return fmt.Errorf("automatic review intent source mismatch")
		}
		if time.Now().Before(intent.NotBefore) {
			continue
		}
		result, err := a.service.Submit(ctx, intent.RecordID)
		if errors.Is(err, core.ErrNoAttachments) || errors.Is(err, core.ErrSourceRemoved) {
			if ackErr := a.store.AcknowledgeAutomaticReview(ctx, intent); ackErr != nil {
				failures = append(failures, ackErr)
			} else {
				a.logger.InfoContext(ctx, "automatic review source inactive", "record_id", intent.RecordID, "source_scope", a.scope)
			}
			continue
		}
		if err == nil {
			err = a.store.AcknowledgeAutomaticReview(ctx, intent)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("automatic review record %s: %w", intent.RecordID, err))
			continue
		}
		a.logger.InfoContext(ctx, "automatic review submitted", "record_id", intent.RecordID, "document_id", result.DocumentID, "status", result.Status, "provider", a.service.options.Provider)
	}
	return errors.Join(failures...)
}

func (a *Automatic) Run(ctx context.Context) {
	for {
		iterationStarted := time.Now()
		if err := a.ProcessPending(ctx); err != nil && ctx.Err() == nil {
			a.logger.WarnContext(ctx, "automatic review remains pending", "error", err)
		}
		// Wake when an edit's quiet period ends, while ordinary recovery keeps
		// its existing 30-second interval. Restart preserves the saved deadline.
		delay := 30 * time.Second
		if intents, err := a.store.PendingAutomaticReviews(ctx, a.scope); err == nil {
			for _, intent := range intents {
				if remaining := time.Until(intent.NotBefore); remaining > 0 && remaining < delay {
					delay = remaining
				} else if remaining <= 0 && intent.NotBefore.After(iterationStarted) {
					// The deadline expired while another record was processed.
					delay = 0
				}
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-a.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
