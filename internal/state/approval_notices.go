package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

func approvalNoticeKey(n core.Notice) string {
	identity, _ := json.Marshal([2]string{n.TargetScope, n.ID})
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:])
}
func noticeMatchesAttempt(n core.Notice, a core.Attempt) bool {
	return n.PlanID == a.Plan.ID && n.SourceScope == a.Plan.SourceScope && n.TargetScope == a.Plan.TargetScope && n.Template == a.Plan.Template
}

// QueueApprovalNotice binds a trigger to an existing plan in the same registry
// transaction. A duplicate never reopens an acknowledged query; conflicting IDs
// cannot redirect it to another native instance.
func (s *Files) QueueApprovalNotice(ctx context.Context, n core.Notice) error {
	if err := n.Validate(); err != nil {
		return err
	}
	return s.Transaction(ctx, approvalRegistryKey(n.SourceScope), func(raw json.RawMessage) (any, error) {
		registry, err := decodeApprovalRegistry(raw, n.SourceScope)
		if err != nil {
			return nil, err
		}
		a, exists := registry.Attempts[n.PlanID]
		if !exists {
			return nil, core.ErrUnknown
		}
		if !noticeMatchesAttempt(n, a) || a.NotSent() || (a.Instance != nil && a.Instance.ID != n.InstanceID) {
			return nil, core.ErrConflict
		}
		key := approvalNoticeKey(n)
		if old, exists := registry.Notices[key]; exists {
			incoming := n
			incoming.ReceivedAt, incoming.ProcessedAt, incoming.Pending, incoming.Failure = old.ReceivedAt, old.ProcessedAt, old.Pending, old.Failure
			if !reflect.DeepEqual(incoming, old) {
				return nil, core.ErrConflict
			}
			return nil, nil
		}
		n.ReceivedAt, n.ProcessedAt, n.Pending, n.Failure = time.Now().UTC(), time.Time{}, true, nil
		if registry.Notices == nil {
			registry.Notices = map[string]core.Notice{}
		}
		registry.Notices[key] = n
		return registry, nil
	})
}

func (s *Files) PendingApprovalNotices(ctx context.Context, scope, target string) ([]core.Notice, error) {
	registry, err := s.readApprovalRegistry(ctx, scope)
	if err != nil {
		return nil, err
	}
	notices := []core.Notice{}
	for _, n := range registry.Notices {
		if n.Pending && n.TargetScope == target {
			notices = append(notices, n)
		}
	}
	sort.Slice(notices, func(i, j int) bool {
		if notices[i].ReceivedAt.Equal(notices[j].ReceivedAt) {
			return notices[i].ID < notices[j].ID
		}
		return notices[i].ReceivedAt.Before(notices[j].ReceivedAt)
	})
	return notices, nil
}

// FinishApprovalNotice runs only after the actual lookup/update. Query failures
// remain pending across restarts. Acknowledgement requires a verified matching
// instance already persisted; it does not release any financial reservation.
func (s *Files) FinishApprovalNotice(ctx context.Context, n core.Notice, failure *core.Failure) error {
	return s.Transaction(ctx, approvalRegistryKey(n.SourceScope), func(raw json.RawMessage) (any, error) {
		registry, err := decodeApprovalRegistry(raw, n.SourceScope)
		if err != nil {
			return nil, err
		}
		key := approvalNoticeKey(n)
		old, exists := registry.Notices[key]
		if !exists {
			return nil, core.ErrUnknown
		}
		if !noticeMatchesAttempt(n, registry.Attempts[old.PlanID]) || n.ID != old.ID || n.InstanceID != old.InstanceID {
			return nil, core.ErrConflict
		}
		if !old.Pending {
			return nil, nil
		}
		if failure != nil {
			copy := *failure
			old.Failure = &copy
		} else {
			a := registry.Attempts[old.PlanID]
			if a.Instance == nil || !a.Instance.Verified || a.Instance.ID != old.InstanceID {
				return nil, core.ErrReconcile
			}
			old.Pending, old.ProcessedAt, old.Failure = false, time.Now().UTC(), nil
		}
		if old.Validate() != nil {
			return nil, core.ErrConflict
		}
		registry.Notices[key] = old
		return registry, nil
	})
}
