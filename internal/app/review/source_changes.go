package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type SourceKind string

const (
	TransactionSource SourceKind = "transactions"
	InvoiceSource     SourceKind = "invoice_ledger"
)

// SourceChange is an invalidation notice, not replacement business evidence.
// Hints can locate dependencies; Prepare still reads authoritative live facts.
type SourceChange struct {
	Scope          string     `json:"review_scope"`
	Source         string     `json:"source"`
	Kind           SourceKind `json:"source_kind"`
	RecordID       string     `json:"record_id"`
	EventID        string     `json:"event_id"`
	InvoiceNumbers []string   `json:"invoice_numbers,omitempty"`
	AllDetails     bool       `json:"all_details,omitempty"`
	ReceivedAt     time.Time  `json:"received_at"`
	Pending        bool       `json:"pending"`
}

func (c SourceChange) Validate() error {
	if strings.TrimSpace(c.Scope) == "" || strings.TrimSpace(c.Source) == "" || strings.TrimSpace(c.RecordID) == "" || strings.TrimSpace(c.EventID) == "" {
		return fmt.Errorf("review source change requires scope, source, record and event")
	}
	if c.Kind != TransactionSource && c.Kind != InvoiceSource {
		return fmt.Errorf("unknown review source kind")
	}
	if c.Kind == TransactionSource && (len(c.InvoiceNumbers) != 0 || c.AllDetails) {
		return fmt.Errorf("transaction changes cannot invalidate invoice-number dependencies")
	}
	return nil
}

type SourceChangeStore interface {
	QueueReviewSourceChange(context.Context, SourceChange) error
	PendingReviewSourceChanges(context.Context, string) ([]SourceChange, error)
	AcknowledgeReviewSourceChange(context.Context, SourceChange) error
	ReviewAttempts(context.Context, string) ([]core.Attempt, error)
	PendingAutomaticReviews(context.Context, string) ([]AutomaticIntent, error)
}

type ChangeScheduler interface {
	NotifyChangeAt(context.Context, string, string, time.Time) error
}

type SourceChanges struct {
	scope   string
	sources map[SourceKind]string
	store   SourceChangeStore
	target  ChangeScheduler
	logger  *slog.Logger
	wake    chan struct{}
}

func NewSourceChanges(scope string, sources map[SourceKind]string, store SourceChangeStore, target ChangeScheduler, logger *slog.Logger) (*SourceChanges, error) {
	if scope == "" || len(sources) == 0 || store == nil || target == nil {
		return nil, fmt.Errorf("review source changes require scope, source bindings, persistent store and scheduler")
	}
	bindings := make(map[SourceKind]string, len(sources))
	for kind, source := range sources {
		if (kind != TransactionSource && kind != InvoiceSource) || source == "" {
			return nil, fmt.Errorf("invalid review source binding")
		}
		bindings[kind] = source
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SourceChanges{scope: scope, sources: bindings, store: store, target: target, logger: logger, wake: make(chan struct{}, 1)}, nil
}

func (s *SourceChanges) EnqueueSourceChange(ctx context.Context, change SourceChange) error {
	if err := change.Validate(); err != nil {
		return err
	}
	if change.Scope != s.scope || s.sources[change.Kind] != change.Source {
		return fmt.Errorf("review source change does not match configured binding")
	}
	if err := s.store.QueueReviewSourceChange(ctx, change); err != nil {
		return err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// AffectedDetails covers historical dependencies as well as new duplicate
// candidates. An old snapshot can cause an extra read, never a guessed result.
func AffectedDetails(change SourceChange, attempts []core.Attempt) []string {
	matched := make(map[string]bool)
	numbers := make(map[string]bool)
	for _, number := range change.InvoiceNumbers {
		if number != "" {
			numbers[number] = true
		}
	}
	for _, attempt := range attempts {
		r := attempt.Request
		id := r.Document.RecordID
		if id == "" || r.LogicalID != change.Scope+":"+id {
			continue
		}
		hit := false
		switch change.Kind {
		case TransactionSource:
			if r.Transactions == nil || r.Transactions.Source != change.Source {
				continue
			}
			for _, id := range r.Transactions.LinkedRecordIDs {
				if id == change.RecordID {
					hit = true
				}
			}
			for _, payment := range r.Transactions.Transactions {
				if payment.RecordID == change.RecordID {
					hit = true
				}
			}
		case InvoiceSource:
			hit = change.AllDetails
			for _, invoice := range r.Document.Invoices {
				if invoice.LedgerRecordID == change.RecordID || numbers[invoice.Facts.Number] {
					hit = true
				}
				for _, candidate := range invoice.Candidates {
					if candidate.RecordID == change.RecordID {
						hit = true
					}
				}
			}
		}
		if hit {
			matched[id] = true
		}
	}
	ids := make([]string, 0, len(matched))
	for id := range matched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *SourceChanges) ProcessPending(ctx context.Context) error {
	changes, err := s.store.PendingReviewSourceChanges(ctx, s.scope)
	if err != nil || len(changes) == 0 {
		return err
	}
	// Read pending work before snapshots: a first Submit can finish and clear
	// its intent while this scan runs. Its old dependency snapshot may not have
	// reached Begin yet, so conservatively invalidate these in-flight details.
	intents, err := s.store.PendingAutomaticReviews(ctx, s.scope)
	if err != nil {
		return err
	}
	attempts, err := s.store.ReviewAttempts(ctx, s.scope)
	if err != nil {
		return err
	}
	var failures []error
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := change.Validate(); err != nil {
			failures = append(failures, err)
			continue
		}
		// A changed configuration cannot redirect archived notices into another
		// table. Keep those notices for a later return to their original binding.
		if change.Scope != s.scope || s.sources[change.Kind] != change.Source {
			continue
		}
		ids := AffectedDetails(change, attempts)
		known := make(map[string]bool, len(ids))
		for _, id := range ids {
			known[id] = true
		}
		for _, intent := range intents {
			if intent.SourceScope == s.scope && intent.Pending && intent.RecordID != "" && !known[intent.RecordID] {
				ids = append(ids, intent.RecordID)
				known[intent.RecordID] = true
			}
		}
		sort.Strings(ids)
		key := "source-change:" + change.Source + ":" + change.RecordID + ":" + change.EventID
		var queueErr error
		for _, id := range ids {
			if queueErr = s.target.NotifyChangeAt(ctx, id, key, change.ReceivedAt); queueErr != nil {
				break
			}
		}
		if queueErr == nil {
			queueErr = s.store.AcknowledgeReviewSourceChange(ctx, change)
		}
		if queueErr != nil {
			failures = append(failures, fmt.Errorf("review source change %s: %w", change.RecordID, queueErr))
			continue
		}
		s.logger.InfoContext(ctx, "review source change scheduled", "source", change.Source, "record_id", change.RecordID, "event_id", change.EventID, "affected_details", len(ids))
	}
	return errors.Join(failures...)
}

func (s *SourceChanges) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.ProcessPending(ctx); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "review source changes remain pending", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}
