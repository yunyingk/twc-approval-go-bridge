package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

// One source registry serializes both attempt creation and member reservation.
// Keeping them in the same atomic replacement avoids a two-file partial claim.
// This namespace is distinct from recognition/review task envelopes.
type approvalRegistry struct {
	Version  int                     `json:"native_approval_version"`
	Scope    string                  `json:"native_approval_scope"`
	Attempts map[string]core.Attempt `json:"attempts"`
}

func approvalRegistryKey(scope string) string { return "native-approval-scope:" + scope }

func decodeApprovalRegistry(raw []byte, scope string) (approvalRegistry, error) {
	registry := approvalRegistry{Version: 1, Scope: scope, Attempts: map[string]core.Attempt{}}
	if raw == nil {
		return registry, nil
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		return registry, err
	}
	if registry.Version != 1 || registry.Scope != scope || registry.Attempts == nil {
		return registry, fmt.Errorf("native approval registry identity or version mismatch")
	}
	for id, attempt := range registry.Attempts {
		if id != attempt.Plan.ID || attempt.Plan.SourceScope != scope {
			return registry, fmt.Errorf("native approval attempt identity mismatch")
		}
		if err := attempt.Validate(); err != nil {
			return registry, fmt.Errorf("invalid saved native approval attempt: %w", err)
		}
	}
	return registry, nil
}

func (s *Files) BeginApproval(ctx context.Context, plan core.Plan) (core.Attempt, bool, error) {
	if err := plan.Validate(); err != nil {
		return core.Attempt{}, false, err
	}
	var attempt core.Attempt
	created := false
	err := s.Transaction(ctx, approvalRegistryKey(plan.SourceScope), func(raw json.RawMessage) (any, error) {
		registry, err := decodeApprovalRegistry(raw, plan.SourceScope)
		if err != nil {
			return nil, err
		}
		if old, exists := registry.Attempts[plan.ID]; exists {
			if old.Plan.Revision != plan.Revision {
				return nil, core.ErrConflict
			}
			attempt = old
			return nil, nil
		}
		members := map[string]bool{}
		for _, row := range plan.Rows {
			members[row.RecordID] = true
		}
		for _, old := range registry.Attempts {
			// Only a proven rejection before creation has no external occupancy.
			// Approval/cancellation/deletion do not imply settlement release.
			if old.Phase == "failed" && old.Instance == nil {
				continue
			}
			for _, row := range old.Plan.Rows {
				if members[row.RecordID] {
					return nil, fmt.Errorf("record %s in plan %s: %w", row.RecordID, old.Plan.ID, core.ErrMemberReserved)
				}
			}
		}
		now := time.Now().UTC()
		attempt = core.Attempt{Plan: plan, Phase: "submitting", CreatedAt: now, UpdatedAt: now}
		registry.Attempts[plan.ID], created = attempt, true
		return registry, nil
	})
	return attempt, created, err
}

func (s *Files) readApprovalRegistry(ctx context.Context, scope string) (approvalRegistry, error) {
	if err := ctx.Err(); err != nil {
		return approvalRegistry{}, err
	}
	if scope == "" {
		return approvalRegistry{}, fmt.Errorf("native approval source scope is required")
	}
	hash := sha256.Sum256([]byte(approvalRegistryKey(scope)))
	raw, err := os.ReadFile(filepath.Join(s.root, hex.EncodeToString(hash[:])+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return decodeApprovalRegistry(nil, scope)
	}
	if err != nil {
		return approvalRegistry{}, err
	}
	return decodeApprovalRegistry(raw, scope)
}

func (s *Files) ReadApproval(ctx context.Context, scope, id string) (core.Attempt, error) {
	registry, err := s.readApprovalRegistry(ctx, scope)
	if err != nil {
		return core.Attempt{}, err
	}
	attempt, exists := registry.Attempts[id]
	if !exists {
		return core.Attempt{}, core.ErrUnknown
	}
	return attempt, nil
}

func (s *Files) ApprovalAttempts(ctx context.Context, scope string) ([]core.Attempt, error) {
	registry, err := s.readApprovalRegistry(ctx, scope)
	if err != nil {
		return nil, err
	}
	attempts := make([]core.Attempt, 0, len(registry.Attempts))
	for _, attempt := range registry.Attempts {
		attempts = append(attempts, attempt)
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].Plan.ID < attempts[j].Plan.ID })
	return attempts, nil
}

func (s *Files) UpdateApproval(ctx context.Context, scope, id string, update func(*core.Attempt) error) (core.Attempt, error) {
	var attempt core.Attempt
	err := s.Transaction(ctx, approvalRegistryKey(scope), func(raw json.RawMessage) (any, error) {
		registry, err := decodeApprovalRegistry(raw, scope)
		if err != nil {
			return nil, err
		}
		var exists bool
		attempt, exists = registry.Attempts[id]
		if !exists {
			return nil, core.ErrUnknown
		}
		before, _ := json.Marshal(attempt.Plan)
		beforeAudit, _ := json.Marshal(attempt.Audit)
		beforeRuns, _ := json.Marshal(attempt.ClosedRuns)
		beforeProof, _ := json.Marshal(attempt.NoCreation)
		oldProof := attempt.NoCreation != nil
		run, runStartedAt, sendToken := attempt.Run, attempt.RunStartedAt, attempt.SendToken
		createdAt, lastObservedAt, oldPhase := attempt.CreatedAt, attempt.LastObservedAt, attempt.Phase
		oldHistory, _ := json.Marshal(attempt.History)
		oldHistoryLen := len(attempt.History)
		var oldInstance *core.Instance
		if attempt.Instance != nil {
			copy := *attempt.Instance
			oldInstance = &copy
		}
		if err := update(&attempt); err != nil {
			return nil, err
		}
		after, err := json.Marshal(attempt.Plan)
		afterAudit, auditErr := json.Marshal(attempt.Audit)
		afterRuns, _ := json.Marshal(attempt.ClosedRuns)
		afterProof, _ := json.Marshal(attempt.NoCreation)
		if err != nil || auditErr != nil || string(before) != string(after) || string(beforeAudit) != string(afterAudit) {
			return nil, fmt.Errorf("native approval frozen plan cannot be modified")
		}
		if run != attempt.Run || runStartedAt != attempt.RunStartedAt || sendToken != attempt.SendToken ||
			string(beforeRuns) != string(afterRuns) || (oldProof && string(beforeProof) != string(afterProof)) {
			return nil, core.ErrConflict
		}
		if !oldProof && attempt.NoCreation != nil && !((oldPhase == "submitting" && attempt.Phase == "failed" && attempt.NoCreation.Kind == "rejected") ||
			(oldPhase == "reserved" && attempt.Phase == "failed" && attempt.NoCreation.Kind == "abandoned")) {
			return nil, core.ErrConflict
		}
		if !oldProof && attempt.NoCreation != nil && (attempt.Failure == nil || attempt.NoCreation.Failure != *attempt.Failure) {
			return nil, core.ErrConflict
		}
		// A late update cannot turn an uncertain external call into a proven
		// rejection, forget an observation, or release a confirmed reservation.
		if attempt.CreatedAt != createdAt || attempt.LastObservedAt.Before(lastObservedAt) || len(attempt.History) < oldHistoryLen ||
			!validApprovalTransition(oldPhase, attempt.Phase) {
			return nil, core.ErrConflict
		}
		if oldHistoryLen > 0 {
			prefix, _ := json.Marshal(attempt.History[:oldHistoryLen])
			if string(prefix) != string(oldHistory) {
				return nil, core.ErrConflict
			}
		}
		if oldInstance != nil && (attempt.Instance == nil || oldInstance.ID != attempt.Instance.ID) {
			return nil, core.ErrConflict
		}
		if oldInstance != nil && ((oldInstance.Verified && !attempt.Instance.Verified) || (oldInstance.Status != "pending" && attempt.Instance.Status == "pending")) {
			return nil, core.ErrConflict
		}
		if attempt.Phase == "failed" && oldPhase == "submitting" && (attempt.Failure == nil || attempt.Failure.Phase != "creation" || attempt.Failure.Code != "failed") {
			return nil, core.ErrConflict
		}
		if attempt.Phase == "failed" && oldPhase == "reserved" && (attempt.Failure == nil || attempt.Failure.Phase != "preparation" || attempt.Failure.Code != "abandoned") {
			return nil, core.ErrConflict
		}
		attempt.UpdatedAt = time.Now().UTC()
		if err := attempt.Validate(); err != nil {
			return nil, err
		}
		registry.Attempts[id] = attempt
		return registry, nil
	})
	return attempt, err
}

func validApprovalTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "reserved":
		return to == "submitting" || to == "failed"
	case "submitting":
		return to == "unknown" || to == "failed" || to == "pending" || to == "finished"
	case "unknown", "failed":
		return to == "pending" || to == "finished"
	case "pending":
		return to == "finished"
	}
	return false
}
