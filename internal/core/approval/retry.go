package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// NoCreationProof is separate from the latest diagnostic failure: an unsuccessful
// UUID lookup cannot erase the original rejection or prove an uncertain POST absent.
type NoCreationProof struct {
	Kind    string  `json:"kind"` // rejected, abandoned, not_sent
	Failure Failure `json:"failure"`
}

func (p NoCreationProof) Validate() error {
	f := p.Failure
	if f.OccurredAt.IsZero() || (f.HTTPStatus != 0 && (f.HTTPStatus < 100 || f.HTTPStatus > 599)) ||
		len(f.RemoteCode) > 20 || strings.Trim(f.RemoteCode, "0123456789") != "" {
		return ErrConflict
	}
	switch p.Kind {
	case "rejected":
		if f.Phase == "creation" && f.Code == "failed" {
			return nil
		}
	case "abandoned":
		if f.Phase == "preparation" && f.Code == "abandoned" {
			return nil
		}
	case "not_sent":
		if f.Phase == "persistence" && f.Code == "not_sent" {
			return nil
		}
	}
	return ErrConflict
}

// ClosedRun preserves each proven unsuccessful run of the same frozen request.
// The outer immutable Audit remains the reference for every run; no request or
// UUID is replaced by retry. LastFailure may be a later lookup diagnostic.
type ClosedRun struct {
	Run         uint64          `json:"run"`
	StartedAt   time.Time       `json:"started_at"`
	FinishedAt  time.Time       `json:"finished_at"`
	Proof       NoCreationProof `json:"no_creation"`
	LastFailure *Failure        `json:"last_failure,omitempty"`
}

func ValidSendToken(token string) bool {
	decoded, err := hex.DecodeString(token)
	return err == nil && len(decoded) == sha256.Size && token == hex.EncodeToString(decoded)
}

func (a Attempt) RunStart() time.Time {
	if a.RunStartedAt.IsZero() {
		return a.CreatedAt
	}
	return a.RunStartedAt
}

// RetryProof supports old attempts only when their original, timestamped failure
// is still present. A legacy failed phase or a not-found query alone is insufficient.
func (a Attempt) RetryProof() *NoCreationProof {
	if a.Phase != "failed" || a.Instance != nil || a.Audit == nil {
		return nil
	}
	if a.NoCreation != nil {
		proof := *a.NoCreation
		if proof.Validate() == nil {
			return &proof
		}
		return nil
	}
	if a.Failure == nil || a.Failure.OccurredAt.Before(a.RunStart()) || a.Failure.OccurredAt.After(a.UpdatedAt) {
		return nil
	}
	for _, kind := range []string{"rejected", "abandoned"} {
		proof := NoCreationProof{Kind: kind, Failure: *a.Failure}
		if proof.Validate() == nil {
			return &proof
		}
	}
	return nil
}

func (a Attempt) validateRuns() error {
	if a.SendToken != "" && (!ValidSendToken(a.SendToken) || a.Audit == nil || a.Phase == "reserved") {
		return ErrConflict
	}
	if uint64(len(a.ClosedRuns)) != a.Run || a.RunStart().Before(a.CreatedAt) || a.RunStart().After(a.UpdatedAt) ||
		(a.Run > 0 && (a.Audit == nil || a.RunStartedAt.IsZero())) {
		return ErrConflict
	}
	previous := a.CreatedAt
	for n, run := range a.ClosedRuns {
		if run.Run != uint64(n) || run.Proof.Validate() != nil || run.StartedAt.Before(previous) || run.FinishedAt.Before(run.StartedAt) ||
			run.Proof.Failure.OccurredAt.Before(run.StartedAt) || run.Proof.Failure.OccurredAt.After(run.FinishedAt) {
			return ErrConflict
		}
		previous = run.FinishedAt
	}
	if a.RunStart().Before(previous) {
		return ErrConflict
	}
	if a.NoCreation != nil && (a.NoCreation.Validate() != nil ||
		(a.Phase != "failed" && a.Instance == nil) || a.NoCreation.Failure.OccurredAt.Before(a.RunStart()) || a.NoCreation.Failure.OccurredAt.After(a.UpdatedAt)) {
		return ErrConflict
	}
	if a.NoCreation != nil && ((a.NoCreation.Kind == "not_sent" && a.SendToken == "") || (a.NoCreation.Kind == "abandoned" && a.SendToken != "")) {
		return ErrConflict
	}
	return nil
}
