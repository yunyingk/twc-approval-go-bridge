package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

func matchingPreparedAttempt(attempt core.Attempt, prepared core.PreparedPlan, batchID string) bool {
	return attempt.Plan.ID == prepared.Plan.ID && attempt.Plan.Revision == prepared.Plan.Revision &&
		attempt.Audit != nil && *attempt.Audit == prepared.AuditReference(batchID)
}

// BeginApprovalBatch reserves every new group in one source-registry replacement.
// The private audit must already exist. No group has a send intent yet: reserved
// means a crash/restart can safely resume, without treating an unsent RPC as lost.
func (s *Files) BeginApprovalBatch(ctx context.Context, id string) ([]core.Attempt, error) {
	batch, err := s.ReadApprovalPreparation(ctx, id)
	if err != nil {
		return nil, err
	}
	scope := batch.Plans[0].Plan.SourceScope
	var attempts []core.Attempt
	err = s.Transaction(ctx, approvalRegistryKey(scope), func(raw json.RawMessage) (any, error) {
		// Recheck the immutable file inside the reservation transaction. Missing
		// or damaged private evidence cannot silently acquire native send rights.
		if _, err := s.ReadApprovalPreparation(ctx, id); err != nil {
			return nil, err
		}
		registry, err := decodeApprovalRegistry(raw, scope)
		if err != nil {
			return nil, err
		}
		members := map[string]bool{}
		for _, prepared := range batch.Plans {
			if old, exists := registry.Attempts[prepared.Plan.ID]; exists {
				if !matchingPreparedAttempt(old, prepared, id) {
					return nil, core.ErrConflict
				}
				if old.Phase != "reserved" && old.Instance == nil {
					return nil, core.ErrReconcile
				}
				continue
			}
			for _, row := range prepared.Plan.Rows {
				members[row.RecordID] = true
			}
		}
		for _, old := range registry.Attempts {
			if old.Phase == "failed" && old.Instance == nil {
				continue
			}
			for _, row := range old.Plan.Rows {
				if members[row.RecordID] {
					return nil, core.ErrMemberReserved
				}
			}
		}
		now := time.Now().UTC()
		changed := false
		attempts = make([]core.Attempt, 0, len(batch.Plans))
		for _, prepared := range batch.Plans {
			attempt, exists := registry.Attempts[prepared.Plan.ID]
			if !exists {
				audit := prepared.AuditReference(id)
				attempt = core.Attempt{Plan: prepared.Plan, Audit: &audit, Phase: "reserved", CreatedAt: now, UpdatedAt: now}
				if err := attempt.Validate(); err != nil {
					return nil, err
				}
				registry.Attempts[prepared.Plan.ID], changed = attempt, true
			}
			attempts = append(attempts, attempt)
		}
		if !changed {
			return nil, nil
		}
		return registry, nil
	})
	return attempts, err
}

// ClaimApprovalSend atomically changes reserved to submitting. Exactly one
// caller may send. An uncertain local save keeps submitting; it never gives a
// second caller permission to POST merely because no response was observed.
func (s *Files) ClaimApprovalSend(ctx context.Context, scope, id string, audit core.AuditReference) (core.Attempt, bool, error) {
	if audit.Validate() != nil {
		return core.Attempt{}, false, core.ErrConflict
	}
	var attempt core.Attempt
	claimed := false
	err := s.Transaction(ctx, approvalRegistryKey(scope), func(raw json.RawMessage) (any, error) {
		registry, err := decodeApprovalRegistry(raw, scope)
		if err != nil {
			return nil, err
		}
		old, exists := registry.Attempts[id]
		if !exists {
			return nil, core.ErrUnknown
		}
		if old.Audit == nil || *old.Audit != audit {
			return nil, core.ErrConflict
		}
		attempt = old
		if old.Phase != "reserved" {
			return nil, nil
		}
		batch, err := s.ReadApprovalPreparation(ctx, audit.PreparationID)
		if err != nil {
			return nil, err
		}
		found := false
		for _, prepared := range batch.Plans {
			found = found || matchingPreparedAttempt(old, prepared, batch.ID)
		}
		if !found {
			return nil, core.ErrConflict
		}
		attempt.Phase, attempt.UpdatedAt = "submitting", time.Now().UTC()
		registry.Attempts[id], claimed = attempt, true
		return registry, nil
	})
	return attempt, claimed, err
}

// AbandonApprovalReservations proves only that these reserved groups were never
// sent. It cannot cancel/forget submitting, unknown or confirmed remote instances.
func (s *Files) AbandonApprovalReservations(ctx context.Context, id string) ([]core.Attempt, error) {
	batch, err := s.ReadApprovalPreparation(ctx, id)
	if err != nil {
		return nil, err
	}
	var attempts []core.Attempt
	err = s.Transaction(ctx, approvalRegistryKey(batch.Plans[0].Plan.SourceScope), func(raw json.RawMessage) (any, error) {
		registry, err := decodeApprovalRegistry(raw, batch.Plans[0].Plan.SourceScope)
		if err != nil {
			return nil, err
		}
		for _, prepared := range batch.Plans {
			if old, exists := registry.Attempts[prepared.Plan.ID]; exists && !matchingPreparedAttempt(old, prepared, id) {
				return nil, core.ErrConflict
			}
		}
		changed := false
		for _, prepared := range batch.Plans {
			attempt, exists := registry.Attempts[prepared.Plan.ID]
			if !exists {
				continue
			}
			if attempt.Phase == "reserved" {
				now := time.Now().UTC()
				attempt.Phase, attempt.UpdatedAt = "failed", now
				attempt.Failure = &core.Failure{Phase: "preparation", Code: "abandoned", OccurredAt: now}
				if err := attempt.Validate(); err != nil {
					return nil, fmt.Errorf("invalid unsent approval abandonment: %w", err)
				}
				registry.Attempts[prepared.Plan.ID], changed = attempt, true
			}
			attempts = append(attempts, attempt)
		}
		if !changed {
			return nil, nil
		}
		return registry, nil
	})
	return attempts, err
}
