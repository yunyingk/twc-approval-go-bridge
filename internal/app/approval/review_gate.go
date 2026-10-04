package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type ReviewSource interface {
	appreview.Source
	appreview.ScopedSource
}
type ReviewReader interface {
	ReviewAttempts(context.Context, string) ([]review.Attempt, error)
}
type ReviewEvidence struct {
	Reference core.ReviewRef
	Current   review.Request
	Outcome   review.Outcome
}
type ReviewCheck struct {
	RecordID     string          `json:"record_id"`
	Code         string          `json:"code"` // current, no_review, review_stale, review_pending, etc.
	DocumentID   string          `json:"document_id,omitempty"`
	Revision     string          `json:"revision,omitempty"`
	Decision     string          `json:"decision,omitempty"`
	CurrentFacts bool            `json:"current_facts"`
	Evidence     *ReviewEvidence `json:"-"` // Current binary/facts never enter diagnostic JSON.
}
type ReviewGate struct {
	source          ReviewSource
	store           ReviewReader
	scope, provider string
	allowed         []string
}

// NewReviewGate accepts only read ports. A delivered AI column is not a gate;
// saved results must match current source evidence under their original pins.
func NewReviewGate(source ReviewSource, store ReviewReader, scope, provider string, allowed []string) (*ReviewGate, error) {
	if source == nil || store == nil || scope == "" || provider == "" {
		return nil, fmt.Errorf("approval AI gate requires scoped source and readable state")
	}
	seen := map[string]bool{}
	for _, decision := range allowed {
		if (review.Outcome{Decision: decision}).Validate() != nil || seen[decision] {
			return nil, fmt.Errorf("approval AI decision policy is invalid or duplicated")
		}
		seen[decision] = true
	}
	return &ReviewGate{source, store, scope, provider, append([]string(nil), allowed...)}, nil
}

func validSavedReview(a review.Attempt, scope, recordID string) bool {
	r := a.Request
	hash, err := hex.DecodeString(r.Revision)
	if err != nil || len(hash) != sha256.Size || r.LogicalID != scope+":"+recordID || r.Document.RecordID != recordID ||
		r.Document.DocumentID != r.LogicalID+":v:"+r.Revision {
		return false
	}
	switch a.State {
	case "submitting", "unknown", "failed", "pending":
		return true
	case "completed":
		return a.Submission != nil && a.Submission.Status == "completed" && a.Submission.DocumentID == r.Document.DocumentID &&
			a.Submission.Outcome != nil && a.Submission.Outcome.Validate() == nil
	}
	return false
}

// Check reads state once and checks all explicit selections, including blocked
// rows. Multiple current pins are ambiguous even if their suggestions agree.
// Older facts are ignored; an unresolved current attempt cannot be bypassed.
func (g *ReviewGate) Check(ctx context.Context, ids []string) ([]ReviewCheck, error) {
	selected := map[string]bool{}
	for _, id := range ids {
		if id == "" || id != strings.TrimSpace(id) || selected[id] {
			return nil, fmt.Errorf("approval AI gate requires unique selected record IDs")
		}
		if g.source.ReviewLogicalID(id) != g.scope+":"+id {
			return nil, fmt.Errorf("approval AI gate source mismatch")
		}
		selected[id] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("approval AI gate requires selected records")
	}
	attempts, err := g.store.ReviewAttempts(ctx, g.scope)
	if err != nil {
		return nil, fmt.Errorf("read approval AI gate state: %w", err)
	}
	byRecord := map[string][]review.Attempt{}
	for _, a := range attempts {
		if selected[a.Request.Document.RecordID] && a.Request.Provider == g.provider {
			byRecord[a.Request.Document.RecordID] = append(byRecord[a.Request.Document.RecordID], a)
		}
	}
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	checks := make([]ReviewCheck, 0, len(ids))
	for _, id := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		checks = append(checks, g.checkRecord(ctx, id, byRecord[id]))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return checks, nil
}

func (g *ReviewGate) checkRecord(ctx context.Context, id string, attempts []review.Attempt) ReviewCheck {
	check := ReviewCheck{RecordID: id, Code: "no_review"}
	if len(attempts) == 0 {
		return check
	}
	type prepared struct {
		request review.Request
		err     error
	}
	cache := map[string]prepared{}
	type current struct {
		saved   review.Attempt
		request review.Request
	}
	currentAttempts := []current{}
	check.Code = "review_stale"
	for _, a := range attempts {
		if !validSavedReview(a, g.scope, id) {
			check.Code = "review_state_invalid"
			return check
		}
		key := a.Request.ProviderVersion + "\x00" + a.Request.RulesVersion
		p, exists := cache[key]
		if !exists {
			p.request, p.err = appreview.PreparePinned(ctx, g.source, a.Request)
			cache[key] = p
		}
		if p.err != nil {
			check.Code = gatePreparationCode(p.err)
			return check
		}
		if p.request.Revision == a.Request.Revision {
			currentAttempts = append(currentAttempts, current{a, p.request})
		}
	}
	if len(currentAttempts) == 0 {
		return check
	}
	if len(currentAttempts) != 1 {
		check.Code = "review_conflict"
		return check
	}
	a, request := currentAttempts[0].saved, currentAttempts[0].request
	check.DocumentID, check.Revision = a.Request.Document.DocumentID, a.Request.Revision
	check.CurrentFacts = true
	check.Code = "review_" + a.State
	if a.State != "completed" {
		return check
	}
	check.Decision = a.Submission.Outcome.Decision
	if len(g.allowed) == 0 {
		check.Code = "policy_not_configured"
		return check
	}
	if !allowedDecision(g.allowed, check.Decision) {
		check.Code = "decision_not_allowed"
		return check
	}
	check.Code = "current"
	check.Evidence = &ReviewEvidence{Reference: core.ReviewRef{DocumentID: check.DocumentID, Revision: check.Revision, CurrentRevision: request.Revision, State: "completed", Decision: check.Decision}, Current: request, Outcome: *a.Submission.Outcome}
	return check
}

func allowedDecision(allowed []string, decision string) bool {
	for _, item := range allowed {
		if item == decision {
			return true
		}
	}
	return false
}
func gatePreparationCode(err error) string {
	switch {
	case errors.Is(err, review.ErrNoAttachments):
		return "no_attachments"
	case errors.Is(err, review.ErrSourceRemoved):
		return "source_removed"
	case errors.Is(err, review.ErrLedgerIncomplete):
		return "ledger_incomplete"
	case errors.Is(err, review.ErrLedgerAmbiguous):
		return "ledger_conflict"
	default:
		return "source_unavailable"
	}
}
