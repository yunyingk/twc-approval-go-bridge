package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type InspectionSource interface {
	ApprovalSourceScope() string
	Inspect(context.Context, []string) (SourceInspection, error)
}
type RequestRenderer interface {
	TargetScope() string
	PrepareRequest(context.Context, core.Plan) (core.RequestArtifact, error)
}
type PreparationStore interface {
	SaveApprovalPreparation(context.Context, core.PreparedBatch) (core.PreparedBatch, error)
}
type PreparationResult struct {
	Inspection SourceInspection    `json:"inspection"`
	Batch      *core.PreparedBatch `json:"-"`
	Issue      string              `json:"issue,omitempty"`
}
type RequestPreparer struct {
	source   InspectionSource
	renderer RequestRenderer
	store    PreparationStore
	options  core.Options
}

func NewRequestPreparer(source InspectionSource, renderer RequestRenderer, store PreparationStore, options core.Options) (*RequestPreparer, error) {
	if source == nil || renderer == nil || store == nil || source.ApprovalSourceScope() != options.SourceScope || renderer.TargetScope() != options.TargetScope {
		return nil, fmt.Errorf("approval request preparation requires matching explicit source, target and private store")
	}
	options.Axes = append([]string(nil), options.Axes...)
	options.AllowedDecisions = append([]string(nil), options.AllowedDecisions...)
	return &RequestPreparer{source, renderer, store, options}, nil
}
func (p *RequestPreparer) candidate(ctx context.Context, ids []string) (PreparationResult, error) {
	inspection, err := p.source.Inspect(ctx, ids)
	if err != nil {
		return PreparationResult{}, err
	}
	result := PreparationResult{Inspection: inspection}
	rows, err := inspection.Rows()
	if err != nil {
		result.Issue = "source_not_ready"
		return result, nil
	}
	plans, err := core.BuildPlans(p.options, rows)
	if err != nil {
		result.Issue = "plan_inputs_invalid"
		return result, nil
	}
	prepared := make([]core.PreparedPlan, 0, len(plans))
	for _, plan := range plans {
		request, err := p.renderer.PrepareRequest(ctx, plan)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.Issue = "target_request_invalid"
			return result, nil
		}
		prepared = append(prepared, core.PreparedPlan{Plan: plan, Request: request})
	}
	proofs := make([]core.ReviewProof, 0, len(inspection.Reviews))
	for _, check := range inspection.Reviews {
		if check.Evidence == nil {
			result.Issue = "review_proof_missing"
			return result, nil
		}
		proof, err := core.NewReviewProof(check.Evidence.Current, check.Evidence.Outcome)
		if err != nil {
			result.Issue = "review_proof_invalid"
			return result, nil
		}
		proofs = append(proofs, proof)
	}
	batch, err := core.NewPreparedBatch(prepared, proofs)
	if err != nil {
		result.Issue = "preparation_invalid"
		return result, nil
	}
	result.Batch = &batch
	return result, nil
}

// Prepare re-observes the entire source/AI selection and every rendered request
// before atomically saving one private batch. No partial audit, upload, member
// reservation or creation can occur through these ports.
func (p *RequestPreparer) Prepare(ctx context.Context, ids []string) (PreparationResult, error) {
	second, err := p.observe(ctx, ids)
	if err != nil || second.Batch == nil {
		return second, err
	}
	saved, err := p.store.SaveApprovalPreparation(ctx, *second.Batch)
	if err != nil {
		second.Batch = nil
		second.Issue = "preparation_save_failed"
		return second, err
	}
	second.Batch = &saved
	return second, nil
}

// Check re-observes the saved selection under the caller's current configuration
// without saving a replacement audit or invoking any upload/creation method.
func (p *RequestPreparer) Check(ctx context.Context, saved core.PreparedBatch) (PreparationResult, error) {
	if err := saved.Validate(); err != nil {
		return PreparationResult{}, err
	}
	result, err := p.observe(ctx, saved.RecordIDs())
	if err == nil && result.Batch != nil && result.Batch.ID != saved.ID {
		result.Batch = nil
		result.Issue = "preparation_changed"
	}
	return result, err
}

func (p *RequestPreparer) observe(ctx context.Context, ids []string) (PreparationResult, error) {
	first, err := p.candidate(ctx, ids)
	if err != nil || first.Batch == nil {
		return first, err
	}
	second, err := p.candidate(ctx, ids)
	if err != nil {
		return second, err
	}
	if second.Batch == nil || second.Batch.ID != first.Batch.ID {
		second.Batch = nil
		second.Issue = "preparation_changed"
		return second, nil
	}
	return second, nil
}

type PreparedGateway interface {
	Gateway
	RequestRenderer
	CreatePrepared(context.Context, core.Plan, core.RequestArtifact) (core.Instance, error)
}

// AuditedGateway binds ordinary submission/recovery to one exact saved request.
// Source rechecks and atomic member reservations remain in the existing Service.
type AuditedGateway struct {
	gateway  PreparedGateway
	prepared core.PreparedPlan
}

func NewAuditedGateway(gateway PreparedGateway, prepared core.PreparedPlan) (*AuditedGateway, error) {
	if gateway == nil || prepared.Plan.Validate() != nil || prepared.Request.Validate() != nil || gateway.TargetScope() != prepared.Plan.TargetScope {
		return nil, fmt.Errorf("audited approval gateway requires a valid plan and request in the original target")
	}
	// Clone the plan and opaque bytes so caller edits cannot replace the audit.
	raw, err := json.Marshal(prepared)
	if err != nil {
		return nil, err
	}
	var frozen core.PreparedPlan
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	return &AuditedGateway{gateway, frozen}, nil
}
func (g *AuditedGateway) TargetScope() string { return g.gateway.TargetScope() }
func (g *AuditedGateway) Describe(ctx context.Context) (Target, error) {
	return g.gateway.Describe(ctx)
}
func (g *AuditedGateway) Lookup(ctx context.Context, plan core.Plan) (core.Instance, error) {
	return g.gateway.Lookup(ctx, plan)
}
func (g *AuditedGateway) ValidatePlan(ctx context.Context, plan core.Plan) error {
	if plan.ID != g.prepared.Plan.ID || plan.Revision != g.prepared.Plan.Revision || plan.Validate() != nil {
		return core.ErrPlanChanged
	}
	request, err := g.gateway.PrepareRequest(ctx, plan)
	if err != nil {
		return err
	}
	if request.Validate() != nil || request.Format != g.prepared.Request.Format || request.Hash != g.prepared.Request.Hash {
		return core.ErrPlanChanged
	}
	return nil
}
func (g *AuditedGateway) Create(ctx context.Context, plan core.Plan) (core.Instance, error) {
	if plan.ID != g.prepared.Plan.ID || plan.Revision != g.prepared.Plan.Revision || plan.Validate() != nil {
		return core.Instance{}, errors.Join(core.ErrRejected, core.ErrPlanChanged)
	}
	return g.gateway.CreatePrepared(ctx, plan, g.prepared.Request)
}
