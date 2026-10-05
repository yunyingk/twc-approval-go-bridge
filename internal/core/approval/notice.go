package approval

import (
	"fmt"
	"time"
)

// Notice is a durable query trigger for a saved plan. It contains neither an
// asserted decision nor an event payload/token: only the lookup association.
type Notice struct {
	ID          string    `json:"id"`
	SourceScope string    `json:"source_scope"`
	TargetScope string    `json:"target_scope"`
	Template    string    `json:"template"`
	InstanceID  string    `json:"instance_id"`
	PlanID      string    `json:"plan_id,omitempty"`
	ReceivedAt  time.Time `json:"received_at"`
	ProcessedAt time.Time `json:"processed_at,omitempty"`
	Pending     bool      `json:"pending"`
	Failure     *Failure  `json:"failure,omitempty"`
}

func (n Notice) Validate() error {
	for _, value := range []string{n.ID, n.SourceScope, n.TargetScope, n.Template, n.InstanceID} {
		if !validID(value) || len(value) > 512 {
			return fmt.Errorf("approval notice requires explicit bounded lookup identities")
		}
	}
	if n.PlanID != "" && !validID(n.PlanID) {
		return ErrConflict
	}
	if !n.ReceivedAt.IsZero() && n.Pending == !n.ProcessedAt.IsZero() {
		return ErrConflict
	}
	if !n.ProcessedAt.IsZero() && (n.Pending || n.ReceivedAt.IsZero() || n.ProcessedAt.Before(n.ReceivedAt) || n.Failure != nil) {
		return ErrConflict
	}
	if n.Failure != nil && (n.Failure.Phase != "observation" || n.Failure.Code != "lookup_failed" || n.Failure.OccurredAt.IsZero() || !n.Pending) {
		return ErrConflict
	}
	return nil
}
