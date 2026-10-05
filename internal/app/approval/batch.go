package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type BatchReader interface {
	ReadApprovalPreparation(context.Context, string) (core.PreparedBatch, error)
	ReadApproval(context.Context, string, string) (core.Attempt, error)
}
type BatchStore interface {
	BatchReader
	Store
	PreparationStore
	BeginApprovalBatch(context.Context, string) ([]core.Attempt, error)
	ClaimApprovalSend(context.Context, string, string, core.AuditReference) (core.Attempt, bool, error)
}
type BatchSource interface {
	Source
	InspectionSource
}

// BatchStatus intentionally omits private plans, native bodies and AI evidence.
// AllInstancesKnown is a mapping observation, never a financial approval.
type PlanStatus struct {
	ID             string               `json:"plan_id"`
	Revision       string               `json:"revision"`
	RecordIDs      []string             `json:"record_ids"`
	Phase          string               `json:"phase"`
	Audit          *core.AuditReference `json:"audit,omitempty"`
	InstanceID     string               `json:"instance_id,omitempty"`
	InstanceStatus string               `json:"instance_status,omitempty"`
	Verified       bool                 `json:"instance_verified"`
	Failure        *core.Failure        `json:"failure,omitempty"`
	LastObservedAt time.Time            `json:"last_observed_at,omitempty"`
}
type BatchStatus struct {
	ID                string       `json:"preparation_id"`
	SourceScope       string       `json:"source_scope"`
	TargetScope       string       `json:"target_scope"`
	Plans             []PlanStatus `json:"plans"`
	AllInstancesKnown bool         `json:"all_instances_known"`
}
type BatchResult struct {
	Status     BatchStatus       `json:"status"`
	Issue      string            `json:"issue,omitempty"`
	Inspection *SourceInspection `json:"inspection,omitempty"`
}

func ReadBatchStatus(ctx context.Context, store BatchReader, id string) (BatchStatus, error) {
	batch, err := store.ReadApprovalPreparation(ctx, id)
	if err != nil {
		return BatchStatus{}, err
	}
	status := BatchStatus{ID: id, SourceScope: batch.Plans[0].Plan.SourceScope, TargetScope: batch.Plans[0].Plan.TargetScope, AllInstancesKnown: true, Plans: []PlanStatus{}}
	for _, prepared := range batch.Plans {
		plan := prepared.Plan
		row := PlanStatus{ID: plan.ID, Revision: plan.Revision, RecordIDs: []string{}, Phase: "not_started"}
		for _, member := range plan.Rows {
			row.RecordIDs = append(row.RecordIDs, member.RecordID)
		}
		attempt, err := store.ReadApproval(ctx, plan.SourceScope, plan.ID)
		if err != nil && !errors.Is(err, core.ErrUnknown) {
			return BatchStatus{}, err
		}
		if err == nil {
			if attempt.Validate() != nil || attempt.Plan.Revision != plan.Revision {
				return BatchStatus{}, core.ErrConflict
			}
			row.Phase, row.Audit, row.Failure, row.LastObservedAt = attempt.Phase, attempt.Audit, attempt.Failure, attempt.LastObservedAt
			if attempt.Instance != nil {
				row.InstanceID, row.InstanceStatus, row.Verified = attempt.Instance.ID, attempt.Instance.Status, attempt.Instance.Verified
			}
		}
		status.AllInstancesKnown = status.AllInstancesKnown && row.InstanceID != ""
		status.Plans = append(status.Plans, row)
	}
	return status, nil
}

type BatchService struct {
	source   BatchSource
	gateway  PreparedGateway
	store    BatchStore
	preparer *RequestPreparer
}

func NewBatchService(source BatchSource, gateway PreparedGateway, store BatchStore, options core.Options) (*BatchService, error) {
	preparer, err := NewRequestPreparer(source, gateway, store, options)
	if err != nil {
		return nil, err
	}
	return &BatchService{source, gateway, store, preparer}, nil
}

func (s *BatchService) result(ctx context.Context, id, issue string, cause error, inspection *SourceInspection) (BatchResult, error) {
	status, err := ReadBatchStatus(context.WithoutCancel(ctx), s.store, id)
	if err != nil {
		return BatchResult{}, err
	}
	return BatchResult{status, issue, inspection}, cause
}

// Submit checks the complete saved selection before one atomic reservation, then
// claims and sends each group once. A partial failure stops later sends. Confirmed
// mappings survive reruns; uncertainty requires a UUID query before resumption.
func (s *BatchService) Submit(ctx context.Context, id string) (BatchResult, error) {
	batch, err := s.store.ReadApprovalPreparation(ctx, id)
	if err != nil {
		return BatchResult{}, err
	}
	first := batch.Plans[0].Plan
	if first.SourceScope != s.source.ApprovalSourceScope() || first.TargetScope != s.gateway.TargetScope() {
		return s.result(ctx, id, "identity_changed", core.ErrPlanChanged, nil)
	}
	allKnown := true
	for _, prepared := range batch.Plans {
		attempt, err := s.store.ReadApproval(ctx, first.SourceScope, prepared.Plan.ID)
		if errors.Is(err, core.ErrUnknown) {
			allKnown = false
			continue
		}
		if err != nil {
			return BatchResult{}, err
		}
		if attempt.Audit == nil || *attempt.Audit != prepared.AuditReference(id) {
			return s.result(ctx, id, "audit_conflict", core.ErrConflict, nil)
		}
		if attempt.Phase != "reserved" && attempt.Instance == nil {
			return s.result(ctx, id, "reconciliation_required", core.ErrReconcile, nil)
		}
		allKnown = allKnown && attempt.Instance != nil
	}
	if allKnown {
		return s.result(ctx, id, "", nil, nil)
	}
	checked, err := s.preparer.Check(ctx, batch)
	if err != nil {
		return s.result(ctx, id, "source_check_failed", err, &checked.Inspection)
	}
	if checked.Batch == nil {
		return s.result(ctx, id, checked.Issue, core.ErrPlanChanged, &checked.Inspection)
	}
	if _, err := s.store.BeginApprovalBatch(ctx, id); err != nil {
		return s.result(ctx, id, batchFailure(err), err, nil)
	}
	for _, prepared := range batch.Plans {
		old, err := s.store.ReadApproval(ctx, first.SourceScope, prepared.Plan.ID)
		if err != nil {
			return s.result(ctx, id, "state_unavailable", err, nil)
		}
		if old.Instance != nil {
			continue
		}
		audited, err := NewAuditedGateway(s.gateway, prepared)
		if err != nil {
			return s.result(ctx, id, "audit_invalid", err, nil)
		}
		source := &auditedBatchSource{s.source, batch.Reviews}
		plan := prepared.Plan
		service, err := New(source, audited, s.store, Options{SourceScope: plan.SourceScope, DepartmentID: plan.DepartmentID, Submitter: plan.Submitter, Axes: plan.Axes, AllowedDecisions: plan.AllowedDecisions})
		if err != nil {
			return s.result(ctx, id, "submission_configuration_invalid", err, nil)
		}
		ids := make([]string, 0, len(plan.Rows))
		for _, row := range plan.Rows {
			ids = append(ids, row.RecordID)
		}
		current, err := service.Prepare(ctx, ids)
		if err != nil {
			return s.result(ctx, id, "preparation_check_failed", err, nil)
		}
		if len(current) != 1 || current[0].ID != plan.ID {
			return s.result(ctx, id, "preparation_changed", core.ErrPlanChanged, nil)
		}
		attempt, claimed, err := s.store.ClaimApprovalSend(ctx, first.SourceScope, plan.ID, prepared.AuditReference(id))
		if err != nil {
			return s.result(ctx, id, "send_intent_save_failed", err, nil)
		}
		if !claimed {
			if attempt.Instance != nil {
				continue
			}
			return s.result(ctx, id, "reconciliation_required", core.ErrReconcile, nil)
		}
		if _, err := service.send(ctx, plan); err != nil {
			return s.result(ctx, id, batchFailure(err), err, nil)
		}
	}
	return s.result(ctx, id, "", nil, nil)
}

func batchFailure(err error) string {
	switch {
	case errors.Is(err, core.ErrMemberReserved):
		return "member_reserved"
	case errors.Is(err, core.ErrRejected):
		return "creation_rejected"
	case errors.Is(err, core.ErrReconcile):
		return "reconciliation_required"
	case errors.Is(err, core.ErrConflict):
		return "audit_or_instance_conflict"
	default:
		return "submission_failed"
	}
}

// This extra source check compares the complete current proof, including an
// outcome not bound into the native form, before claiming this group's send.
type auditedBatchSource struct {
	source BatchSource
	proofs []core.ReviewProof
}

func (s *auditedBatchSource) ApprovalSourceScope() string { return s.source.ApprovalSourceScope() }
func (s *auditedBatchSource) ReadApprovalRows(ctx context.Context, ids []string) ([]core.Row, error) {
	inspection, err := s.source.Inspect(ctx, ids)
	if err != nil {
		return nil, err
	}
	rows, err := inspection.Rows()
	if err != nil {
		return nil, err
	}
	for _, check := range inspection.Reviews {
		if check.Evidence == nil {
			return nil, core.ErrReviewNotCurrent
		}
		current, err := core.NewReviewProof(check.Evidence.Current, check.Evidence.Outcome)
		if err != nil {
			return nil, err
		}
		found := false
		for _, expected := range s.proofs {
			if expected.Snapshot.Document.RecordID != check.RecordID {
				continue
			}
			a, _ := json.Marshal(current)
			b, _ := json.Marshal(expected)
			found = string(a) == string(b)
		}
		if !found {
			return nil, core.ErrPlanChanged
		}
	}
	return rows, nil
}

// ReconcileBatch uses only saved plans and the original target's lookup port.
// Unsent reservations have no remote UUID to reconcile and are never queried.
func ReconcileBatch(ctx context.Context, store interface {
	BatchReader
	Store
}, lookup LookupGateway, id string) (BatchResult, error) {
	batch, err := store.ReadApprovalPreparation(ctx, id)
	if err != nil {
		return BatchResult{}, err
	}
	if lookup == nil || lookup.TargetScope() != batch.Plans[0].Plan.TargetScope {
		return BatchResult{}, fmt.Errorf("approval reconciliation requires the saved target identity")
	}
	service, err := NewReconciler(lookup, store, batch.Plans[0].Plan.SourceScope)
	if err != nil {
		return BatchResult{}, err
	}
	for _, prepared := range batch.Plans {
		attempt, err := store.ReadApproval(ctx, prepared.Plan.SourceScope, prepared.Plan.ID)
		if err != nil && !errors.Is(err, core.ErrUnknown) {
			return BatchResult{}, err
		}
		if err == nil && attempt.Audit != nil && *attempt.Audit != prepared.AuditReference(id) {
			return BatchResult{}, core.ErrConflict
		}
	}
	issue := ""
	var failed error
	for _, prepared := range batch.Plans {
		attempt, err := store.ReadApproval(ctx, prepared.Plan.SourceScope, prepared.Plan.ID)
		if errors.Is(err, core.ErrUnknown) || (err == nil && attempt.NotSent()) {
			if issue == "" {
				issue = "not_submitted"
			}
			continue
		}
		if err != nil {
			return BatchResult{}, err
		}
		if _, err := service.Reconcile(ctx, prepared.Plan.ID); err != nil {
			issue, failed = "reconciliation_failed", err
			if ctx.Err() != nil {
				break
			}
		}
	}
	status, err := ReadBatchStatus(context.WithoutCancel(ctx), store, id)
	if err != nil {
		return BatchResult{}, err
	}
	return BatchResult{Status: status, Issue: issue}, failed
}
