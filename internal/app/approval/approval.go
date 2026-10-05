// Package approval coordinates grouped human approvals without platform DTOs.
package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type Source interface {
	// The scope identifies the physical source, independent of field mapping,
	// grouping or provider configuration, so those edits cannot bypass reservations.
	ApprovalSourceScope() string
	ReadApprovalRows(context.Context, []string) ([]core.Row, error)
}
type Target struct{ Scope, Template, ConfigurationVersion string }
type LookupGateway interface {
	TargetScope() string
	Lookup(context.Context, core.Plan) (core.Instance, error)
}
type Gateway interface {
	LookupGateway
	Describe(context.Context) (Target, error)
	ValidatePlan(context.Context, core.Plan) error
	Create(context.Context, core.Plan) (core.Instance, error)
}
type Store interface {
	BeginApproval(context.Context, core.Plan) (core.Attempt, bool, error)
	ReadApproval(context.Context, string, string) (core.Attempt, error)
	UpdateApproval(context.Context, string, string, func(*core.Attempt) error) (core.Attempt, error)
}
type Options struct {
	SourceScope, DepartmentID string
	Submitter                 core.Identity
	Axes, AllowedDecisions    []string
}
type Service struct {
	source  Source
	gateway Gateway
	lookup  LookupGateway
	store   Store
	options Options
}

func New(source Source, gateway Gateway, store Store, options Options) (*Service, error) {
	if source == nil || gateway == nil || store == nil || options.SourceScope == "" || source.ApprovalSourceScope() != options.SourceScope || options.Submitter.Scope != gateway.TargetScope() {
		return nil, fmt.Errorf("human approval requires matching source, target identity and persistent store")
	}
	options.Axes = append([]string(nil), options.Axes...)
	options.AllowedDecisions = append([]string(nil), options.AllowedDecisions...)
	return &Service{source: source, gateway: gateway, lookup: gateway, store: store, options: options}, nil
}

// NewReconciler requires no current business source or submission configuration.
// Saved UUIDs can be resolved after templates or source facts have changed.
func NewReconciler(gateway LookupGateway, store Store, sourceScope string) (*Service, error) {
	if gateway == nil || store == nil || sourceScope == "" {
		return nil, fmt.Errorf("approval reconciliation requires gateway, store and source scope")
	}
	return &Service{lookup: gateway, store: store, options: Options{SourceScope: sourceScope}}, nil
}

func (s *Service) Prepare(ctx context.Context, recordIDs []string) ([]core.Plan, error) {
	if s.source == nil || s.gateway == nil || s.source.ApprovalSourceScope() != s.options.SourceScope {
		return nil, fmt.Errorf("approval preparation source is not configured")
	}
	selected := map[string]bool{}
	for _, id := range recordIDs {
		if id == "" || id != strings.TrimSpace(id) || selected[id] {
			return nil, fmt.Errorf("approval requires unique selected record IDs")
		}
		selected[id] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("approval requires selected record IDs")
	}
	rows, err := s.source.ReadApprovalRows(ctx, recordIDs)
	if err != nil {
		return nil, err
	}
	if len(rows) != len(selected) {
		return nil, fmt.Errorf("approval source did not return exactly the selected rows")
	}
	for _, row := range rows {
		if !selected[row.RecordID] {
			return nil, fmt.Errorf("approval source returned an unselected row")
		}
		delete(selected, row.RecordID)
	}
	target, err := s.gateway.Describe(ctx)
	if err != nil {
		return nil, err
	}
	if target.Scope != s.gateway.TargetScope() {
		return nil, fmt.Errorf("approval gateway described another target identity")
	}
	plans, err := core.BuildPlans(core.Options{SourceScope: s.options.SourceScope, TargetScope: target.Scope, Template: target.Template, ConfigurationVersion: target.ConfigurationVersion,
		DepartmentID: s.options.DepartmentID, Submitter: s.options.Submitter, Axes: s.options.Axes, AllowedDecisions: s.options.AllowedDecisions}, rows)
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		if err := s.gateway.ValidatePlan(ctx, plan); err != nil {
			return nil, err
		}
	}
	return plans, nil
}

func (s *Service) checkScope(plan core.Plan) error {
	if plan.SourceScope != s.options.SourceScope || plan.TargetScope != s.lookup.TargetScope() {
		return fmt.Errorf("approval plan belongs to another source or target identity")
	}
	return plan.Validate()
}

func existingAttempt(attempt core.Attempt) (core.Attempt, error) {
	if attempt.Instance != nil {
		return attempt, nil
	}
	return attempt, core.ErrReconcile
}

// Submit rechecks a new plan, reserves all its members atomically, then sends
// once. Existing attempts retain their historical mapping; they are not a live
// approval of current source facts and never cause another creation request.
func (s *Service) Submit(ctx context.Context, plan core.Plan) (core.Attempt, error) {
	if s.source == nil || s.gateway == nil {
		return core.Attempt{}, fmt.Errorf("reconciliation service cannot submit approvals")
	}
	if err := s.checkScope(plan); err != nil {
		return core.Attempt{}, err
	}
	if old, err := s.store.ReadApproval(ctx, plan.SourceScope, plan.ID); err == nil {
		return existingAttempt(old)
	} else if !errors.Is(err, core.ErrUnknown) {
		return core.Attempt{}, err
	}
	ids := make([]string, 0, len(plan.Rows))
	for _, row := range plan.Rows {
		ids = append(ids, row.RecordID)
	}
	current, err := s.Prepare(ctx, ids)
	if err != nil {
		return core.Attempt{}, err
	}
	if len(current) != 1 || current[0].ID != plan.ID {
		return core.Attempt{}, core.ErrPlanChanged
	}
	attempt, created, err := s.store.BeginApproval(ctx, plan)
	if err != nil {
		return core.Attempt{}, err
	}
	if !created {
		return existingAttempt(attempt)
	}
	return s.send(ctx, plan)
}

func (s *Service) send(ctx context.Context, plan core.Plan) (core.Attempt, error) {
	instance, callErr := s.gateway.Create(ctx, plan)
	if callErr == nil {
		callErr = validateInstance(plan, instance)
	}
	attempt, err := s.store.UpdateApproval(context.WithoutCancel(ctx), plan.SourceScope, plan.ID, func(a *core.Attempt) error {
		// A query/callback may confirm the instance before create returns.
		if a.Instance != nil {
			if callErr == nil && a.Instance.ID != instance.ID {
				return core.ErrConflict
			}
			return nil
		}
		if callErr != nil {
			a.Phase = "unknown"
			if errors.Is(callErr, core.ErrRejected) {
				a.Phase = "failed"
			}
			a.Failure = failure("creation", a.Phase, callErr)
			return nil
		}
		a.Instance, a.Phase, a.Failure = &instance, phaseFor(instance.Status), nil
		return nil
	})
	if err != nil {
		return core.Attempt{}, fmt.Errorf("persist approval creation result: %w", err)
	}
	if callErr != nil && attempt.Instance == nil {
		return attempt, callErr
	}
	return attempt, nil
}

func validateInstance(plan core.Plan, instance core.Instance) error {
	return instance.Validate(plan)
}

func phaseFor(status string) string {
	if status == "pending" {
		return "pending"
	}
	return "finished"
}

// Reconcile always queries the saved UUID and checks its full target identity.
// A failed/not-found lookup retains the original reservation, never re-creates.
func (s *Service) Reconcile(ctx context.Context, id string) (core.Attempt, error) {
	attempt, err := s.store.ReadApproval(ctx, s.options.SourceScope, id)
	if err != nil {
		return core.Attempt{}, err
	}
	if err := s.checkScope(attempt.Plan); err != nil {
		return core.Attempt{}, err
	}
	if attempt.NotSent() {
		return attempt, core.ErrNotSubmitted
	}
	started := time.Now().UTC()
	instance, callErr := s.lookup.Lookup(ctx, attempt.Plan)
	if callErr == nil {
		callErr = validateInstance(attempt.Plan, instance)
		if callErr == nil && !instance.Verified {
			callErr = core.ErrConflict
		}
	}
	attempt, err = s.store.UpdateApproval(context.WithoutCancel(ctx), attempt.Plan.SourceScope, id, func(a *core.Attempt) error {
		if a.LastObservedAt.After(started) {
			return nil
		}
		if callErr != nil {
			a.Failure = failure("reconciliation", "lookup_failed", callErr)
			return nil
		}
		if a.Instance != nil && a.Instance.ID != instance.ID {
			return core.ErrConflict
		}
		// A final state cannot regress to pending, including an old query whose
		// response arrives after another request confirmed completion.
		if a.Instance != nil && a.Instance.Status != "pending" && instance.Status == "pending" {
			return core.ErrConflict
		}
		if a.Instance == nil || *a.Instance != instance {
			a.History = append(a.History, core.Observation{Instance: instance, At: started})
		}
		a.Instance, a.Phase, a.Failure, a.LastObservedAt = &instance, phaseFor(instance.Status), nil, started
		return nil
	})
	if err != nil {
		return core.Attempt{}, err
	}
	if callErr != nil {
		return attempt, callErr
	}
	return attempt, nil
}

func failure(phase, code string, err error) *core.Failure {
	f := &core.Failure{Phase: phase, Code: code, OccurredAt: time.Now().UTC()}
	var http interface{ HTTPStatus() int }
	if errors.As(err, &http) {
		f.HTTPStatus = http.HTTPStatus()
	}
	var remote interface{ RemoteErrorCode() string }
	if errors.As(err, &remote) {
		code := remote.RemoteErrorCode()
		if len(code) > 0 && len(code) <= 20 && strings.Trim(code, "0123456789") == "" {
			f.RemoteCode = code
		}
	}
	return f
}
