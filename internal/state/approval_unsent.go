package state

import (
	"context"
	"encoding/json"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

// ProveApprovalNotSent is called only by the owner whose claim returned an error
// and who has not invoked the gateway. A restart, operator or another caller must
// not infer absence from submitting, query failures, or an old send token.
func (s *Files) ProveApprovalNotSent(ctx context.Context, scope, id string, audit core.AuditReference, run uint64, token string) (core.Attempt, error) {
	if audit.Validate() != nil || !core.ValidSendToken(token) {
		return core.Attempt{}, core.ErrConflict
	}
	var attempt core.Attempt
	err := s.Transaction(ctx, approvalRegistryKey(scope), func(raw json.RawMessage) (any, error) {
		registry, err := decodeApprovalRegistry(raw, scope)
		if err != nil {
			return nil, err
		}
		old, exists := registry.Attempts[id]
		if !exists {
			return nil, core.ErrUnknown
		}
		if old.Audit == nil || *old.Audit != audit || old.Run != run || old.SendToken != token || old.Instance != nil {
			return nil, core.ErrConflict
		}
		attempt = old
		if old.Phase == "failed" && old.NoCreation != nil && old.NoCreation.Kind == "not_sent" {
			return nil, nil
		}
		if old.Phase != "submitting" {
			return nil, core.ErrReconcile
		}
		now := time.Now().UTC()
		attempt.Phase, attempt.UpdatedAt = "failed", now
		attempt.Failure = &core.Failure{Phase: "persistence", Code: "not_sent", OccurredAt: now}
		attempt.NoCreation = &core.NoCreationProof{Kind: "not_sent", Failure: *attempt.Failure}
		if err := attempt.Validate(); err != nil {
			return nil, err
		}
		registry.Attempts[id] = attempt
		return registry, nil
	})
	return attempt, err
}
