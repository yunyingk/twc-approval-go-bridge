package approval

import (
	"fmt"
	"strings"
	"time"
)

type Instance struct {
	ID          string `json:"id"`
	UUID        string `json:"uuid"`
	TargetScope string `json:"target_scope"`
	Template    string `json:"template"`
	SubmitterID string `json:"submitter_id"`
	Status      string `json:"status"` // pending, approved, rejected, canceled, deleted
	Verified    bool   `json:"verified"`
}

func (i Instance) Validate(plan Plan) error {
	if !validID(i.ID) || !strings.EqualFold(i.UUID, plan.ID) || i.TargetScope != plan.TargetScope || i.Template != plan.Template || i.SubmitterID != plan.Submitter.ID {
		return ErrConflict
	}
	switch i.Status {
	case "pending":
		return nil
	case "approved", "rejected", "canceled", "deleted":
		if i.Verified {
			return nil
		}
	}
	return ErrConflict
}

type Failure struct {
	Phase      string    `json:"phase"`
	Code       string    `json:"code"`
	HTTPStatus int       `json:"http_status,omitempty"`
	RemoteCode string    `json:"remote_code,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}
type Observation struct {
	Instance Instance  `json:"instance"`
	At       time.Time `json:"at"`
}
type Attempt struct {
	Plan           Plan          `json:"plan"`
	Phase          string        `json:"phase"` // submitting, unknown, failed, pending, finished
	Instance       *Instance     `json:"instance,omitempty"`
	Failure        *Failure      `json:"failure,omitempty"`
	History        []Observation `json:"history,omitempty"`
	LastObservedAt time.Time     `json:"last_observed_at,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// Validate rejects inconsistent saved states before they can release members
// or be used as a confirmed instance mapping.
func (a Attempt) Validate() error {
	if err := a.Plan.Validate(); err != nil {
		return err
	}
	if a.CreatedAt.IsZero() || a.UpdatedAt.Before(a.CreatedAt) {
		return fmt.Errorf("invalid approval attempt timestamps")
	}
	switch a.Phase {
	case "submitting", "unknown", "failed":
		if a.Instance != nil || (a.Phase == "failed" && a.Failure == nil) {
			return ErrConflict
		}
	case "pending", "finished":
		if a.Instance == nil || (a.Phase == "pending") != (a.Instance.Status == "pending") {
			return ErrConflict
		}
	default:
		return fmt.Errorf("invalid approval attempt phase")
	}
	if a.Instance != nil {
		if err := a.Instance.Validate(a.Plan); err != nil {
			return err
		}
	}
	var previous time.Time
	for _, observation := range a.History {
		if !observation.Instance.Verified || observation.Instance.Validate(a.Plan) != nil ||
			observation.At.IsZero() || observation.At.Before(previous) || a.Instance == nil || observation.Instance.ID != a.Instance.ID {
			return ErrConflict
		}
		previous = observation.At
	}
	if len(a.History) > 0 {
		if !a.Instance.Verified || *a.Instance != a.History[len(a.History)-1].Instance || a.LastObservedAt.Before(previous) {
			return ErrConflict
		}
	} else if !a.LastObservedAt.IsZero() {
		return ErrConflict
	}
	return nil
}
