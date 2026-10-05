package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AuditReference binds one send intent to the private, immutable prepared body.
// It is optional only for attempts from the earlier unaudited API.
type AuditReference struct {
	PreparationID string `json:"preparation_id"`
	RequestFormat string `json:"request_format"`
	RequestHash   string `json:"request_sha256"`
}

func (a AuditReference) Validate() error {
	for _, digest := range []string{a.PreparationID, a.RequestHash} {
		bytes, err := hex.DecodeString(digest)
		if err != nil || len(bytes) != sha256.Size || digest != hex.EncodeToString(bytes) {
			return ErrConflict
		}
	}
	if !validID(a.RequestFormat) {
		return ErrConflict
	}
	return nil
}

type Instance struct {
	ID          string `json:"id"`
	UUID        string `json:"uuid"`
	TargetScope string `json:"target_scope"`
	Template    string `json:"template"`
	SubmitterID string `json:"submitter_id"`
	Status      string `json:"status"` // pending, approved, rejected, canceled, deleted, reverted
	Verified    bool   `json:"verified"`
}

func (i Instance) Validate(plan Plan) error {
	if !validID(i.ID) || !strings.EqualFold(i.UUID, plan.ID) || i.TargetScope != plan.TargetScope || i.Template != plan.Template || i.SubmitterID != plan.Submitter.ID {
		return ErrConflict
	}
	switch i.Status {
	case "pending":
		return nil
	case "approved", "rejected", "canceled", "deleted", "reverted":
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
	Plan             Plan                `json:"plan"`
	Audit            *AuditReference     `json:"audit,omitempty"`
	Phase            string              `json:"phase"` // reserved, submitting, unknown, failed, pending, finished
	Instance         *Instance           `json:"instance,omitempty"`
	Failure          *Failure            `json:"failure,omitempty"`
	NoCreation       *NoCreationProof    `json:"no_creation,omitempty"`
	Run              uint64              `json:"run"`
	RunStartedAt     time.Time           `json:"run_started_at,omitempty"`
	SendToken        string              `json:"send_token,omitempty"`
	ClosedRuns       []ClosedRun         `json:"closed_runs,omitempty"`
	History          []Observation       `json:"history,omitempty"`
	Results          []ResultObservation `json:"results,omitempty"`
	ResultObservedAt time.Time           `json:"result_observed_at,omitempty"`
	LastObservedAt   time.Time           `json:"last_observed_at,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

// NotSent is based on a reserved phase or explicit local abandonment proof.
// A later query failure must never turn a previously sent request into unsent.
// An exact owner proof after a failed claim is also evidence of no POST.
func (a Attempt) NotSent() bool {
	if a.Instance != nil {
		return false
	}
	if a.Phase == "failed" && a.NoCreation != nil {
		return a.NoCreation.Kind == "abandoned" || a.NoCreation.Kind == "not_sent"
	}
	return a.Phase == "reserved" || (a.Phase == "failed" && a.Failure != nil && a.Failure.Phase == "preparation" && a.Failure.Code == "abandoned")
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
	if a.Audit != nil && a.Audit.Validate() != nil {
		return ErrConflict
	}
	if err := a.validateRuns(); err != nil {
		return err
	}
	switch a.Phase {
	case "reserved":
		if a.Audit == nil || a.Instance != nil || a.Failure != nil || len(a.History) != 0 {
			return ErrConflict
		}
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
	previous = time.Time{}
	if (len(a.Results) == 0) != a.ResultObservedAt.IsZero() || a.ResultObservedAt.After(a.LastObservedAt) {
		return ErrConflict
	}
	for _, result := range a.Results {
		if result.Snapshot.Validate(a.Plan) != nil || result.At.IsZero() || result.At.Before(previous) || a.Instance == nil || result.Snapshot.Instance.ID != a.Instance.ID || result.At.After(a.ResultObservedAt) {
			return ErrConflict
		}
		previous = result.At
	}
	return nil
}

// CurrentResult never labels older evidence as current after a lookup from a
// gateway that only reports status. Historical private snapshots remain intact.
func (a Attempt) CurrentResult() *ResultSnapshot {
	if a.Instance == nil || a.Failure != nil || len(a.Results) == 0 || !a.ResultObservedAt.Equal(a.LastObservedAt) {
		return nil
	}
	result := a.Results[len(a.Results)-1].Snapshot
	if result.Instance != *a.Instance {
		return nil
	}
	return &result
}
