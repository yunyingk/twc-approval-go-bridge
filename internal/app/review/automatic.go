package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// AutomaticIntent survives a restart between ledger delivery and submission.
// Generation prevents a receipt arriving during submission from being discarded.
type AutomaticIntent struct {
	SourceScope string `json:"source_scope"`
	RecordID    string `json:"record_id"`
	Generation  uint64 `json:"generation"`
	Pending     bool   `json:"pending"`
}

type AutomaticStore interface {
	QueueAutomaticReview(context.Context, string, string) error
	PendingAutomaticReviews(context.Context, string) ([]AutomaticIntent, error)
	AcknowledgeAutomaticReview(context.Context, AutomaticIntent) error
}

type Automatic struct {
	service *Service
	store   AutomaticStore
	scope   string
	logger  *slog.Logger
	wake    chan struct{}
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
		result, err := a.service.Submit(ctx, intent.RecordID)
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
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := a.ProcessPending(ctx); err != nil && ctx.Err() == nil {
			a.logger.WarnContext(ctx, "automatic review remains pending", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-a.wake:
		case <-ticker.C:
		}
	}
}
