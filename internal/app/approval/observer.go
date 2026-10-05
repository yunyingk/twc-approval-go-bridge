package approval

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type ObservationStore interface {
	Store
	ApprovalAttempts(context.Context, string) ([]core.Attempt, error)
	QueueApprovalNotice(context.Context, core.Notice) error
	PendingApprovalNotices(context.Context, string, string) ([]core.Notice, error)
	FinishApprovalNotice(context.Context, core.Notice, *core.Failure) error
}

// Observer has only saved-state and lookup ports: no source, template, AI client
// or creation API is needed to follow historical instances of the selected app.
type Observer struct {
	scope      string
	lookup     LookupGateway
	store      ObservationStore
	reconciler *Service
	logger     *slog.Logger
	wake       chan struct{}
}

func NewObserver(scope string, lookup LookupGateway, store ObservationStore, logger *slog.Logger) (*Observer, error) {
	if scope == "" || lookup == nil || lookup.TargetScope() == "" || store == nil {
		return nil, fmt.Errorf("approval observer requires original source, target and durable state")
	}
	reconciler, err := NewReconciler(lookup, store, scope)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Observer{scope, lookup, store, reconciler, logger, make(chan struct{}, 1)}, nil
}

// Enqueue acknowledges unrelated instances without creating state. For this
// bridge, Begin always precedes POST, so a create-response race still has a saved
// plan UUID. Without UUID, only an already-confirmed native code can locate it.
func (o *Observer) Enqueue(ctx context.Context, n core.Notice) error {
	if n.Validate() != nil || n.SourceScope != o.scope || n.TargetScope != o.lookup.TargetScope() {
		return core.ErrConflict
	}
	attempts, err := o.store.ApprovalAttempts(ctx, o.scope)
	if err != nil {
		return err
	}
	var found *core.Attempt
	for _, a := range attempts {
		if a.Plan.TargetScope != n.TargetScope || a.Plan.Template != n.Template || a.NotSent() {
			continue
		}
		matched := n.PlanID != "" && strings.EqualFold(a.Plan.ID, n.PlanID)
		if n.PlanID == "" {
			matched = a.Instance != nil && a.Instance.ID == n.InstanceID
		}
		if !matched {
			continue
		}
		if found != nil || (a.Instance != nil && a.Instance.ID != n.InstanceID) {
			return core.ErrConflict
		}
		copy := a
		found = &copy
	}
	if found == nil {
		return nil
	}
	n.PlanID = found.Plan.ID
	if err := o.store.QueueApprovalNotice(ctx, n); err != nil {
		return err
	}
	select {
	case o.wake <- struct{}{}:
	default:
	}
	return nil
}

func (o *Observer) ProcessPending(ctx context.Context) error {
	notices, err := o.store.PendingApprovalNotices(ctx, o.scope, o.lookup.TargetScope())
	if err != nil {
		return err
	}
	var failures []error
	for _, n := range notices {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, queryErr := o.reconciler.ReconcileExpected(ctx, n.PlanID, n.InstanceID)
		var diagnostic *core.Failure
		if queryErr != nil {
			diagnostic = failure("observation", "lookup_failed", queryErr)
		}
		if err := o.store.FinishApprovalNotice(context.WithoutCancel(ctx), n, diagnostic); err != nil {
			failures = append(failures, err)
		}
		if queryErr != nil {
			failures = append(failures, queryErr)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("approval observation remains pending")
	}
	return nil
}

// Poll catches offline/missed events, including later cancellation of a finished
// instance. Unsent reservations are skipped; every query uses its saved UUID/app.
func (o *Observer) Poll(ctx context.Context) error {
	attempts, err := o.store.ApprovalAttempts(ctx, o.scope)
	if err != nil {
		return err
	}
	var failures []error
	for _, a := range attempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.Plan.TargetScope != o.lookup.TargetScope() || a.NotSent() {
			continue
		}
		if _, err := o.reconciler.Reconcile(ctx, a.Plan.ID); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("approval polling remains incomplete")
	}
	return nil
}

func (o *Observer) Run(ctx context.Context, interval time.Duration) error {
	if interval < time.Minute {
		return fmt.Errorf("approval polling interval must be at least one minute")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sweep := true
	for {
		if sweep && o.Poll(ctx) != nil && ctx.Err() == nil {
			o.logger.WarnContext(ctx, "approval polling incomplete", "source_scope", o.scope, "target_scope", o.lookup.TargetScope())
		}
		if err := o.ProcessPending(ctx); err != nil && ctx.Err() == nil {
			o.logger.WarnContext(ctx, "approval notices remain pending", "source_scope", o.scope, "target_scope", o.lookup.TargetScope())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-o.wake:
			sweep = false
		case <-ticker.C:
			sweep = true
		}
	}
}
