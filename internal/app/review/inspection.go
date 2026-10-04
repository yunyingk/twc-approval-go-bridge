package review

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type InspectionStore interface {
	ReviewAttempts(context.Context, string) ([]core.Attempt, error)
	PendingAutomaticReviews(context.Context, string) ([]AutomaticIntent, error)
	PendingReviewSourceChanges(context.Context, string) ([]SourceChange, error)
}

type AttemptStatus struct {
	DocumentID     string             `json:"document_id"`
	RecordID       string             `json:"record_id"`
	Revision       string             `json:"revision"`
	Provider       string             `json:"provider"`
	State          string             `json:"state"`
	Delivered      bool               `json:"delivered"`
	Decision       string             `json:"decision,omitempty"`
	ExternalID     string             `json:"external_id,omitempty"`
	Failure        *core.FailureInfo  `json:"failure,omitempty"`
	Delivery       *core.DeliveryInfo `json:"delivery,omitempty"`
	RevisionStatus string             `json:"revision_status"`
	CheckFailure   *core.FailureInfo  `json:"check_failure,omitempty"`
	NextAction     string             `json:"next_action"`
}

type IntentStatus struct {
	RecordID   string    `json:"record_id"`
	Generation uint64    `json:"generation"`
	NotBefore  time.Time `json:"not_before,omitempty"`
}

type SourceNoticeStatus struct {
	Kind       SourceKind `json:"source_kind"`
	Source     string     `json:"source"`
	RecordID   string     `json:"record_id"`
	EventID    string     `json:"event_id"`
	ReceivedAt time.Time  `json:"received_at"`
}

type Inspection struct {
	Scope            string          `json:"scope"`
	RecordID         string          `json:"record_id,omitempty"`
	LiveChecked      bool            `json:"live_checked"`
	WriterConfigured bool            `json:"writer_configured"`
	Attempts         []AttemptStatus `json:"attempts"`
	Automatic        []IntentStatus  `json:"automatic_pending"`
	// Source notices cover the whole scope; their target fanout may not yet
	// have been resolved, even when one detail is selected for inspection.
	SourceInbox      []SourceNoticeStatus `json:"source_inbox_pending"`
	Readiness        string               `json:"readiness,omitempty"`
	ReadinessFailure *core.FailureInfo    `json:"readiness_failure,omitempty"`
}

type Inspector struct {
	source           Source
	store            InspectionStore
	writerConfigured bool
	provider         string
}

// A nil source is valid for local inspection. No reviewer or write capability
// is accepted, so diagnostic commands cannot send a billable review request.
func NewInspector(source Source, store InspectionStore, writerConfigured bool, provider string) (*Inspector, error) {
	if store == nil {
		return nil, fmt.Errorf("review inspection requires a readable store")
	}
	return &Inspector{source: source, store: store, writerConfigured: writerConfigured, provider: provider}, nil
}

func (i *Inspector) Inspect(ctx context.Context, scope, recordID string, live bool) (Inspection, error) {
	if scope == "" || (live && i.source == nil) {
		return Inspection{}, fmt.Errorf("inspection requires scope and a source for live checks")
	}
	attempts, err := i.store.ReviewAttempts(ctx, scope)
	if err != nil {
		return Inspection{}, err
	}
	intents, err := i.store.PendingAutomaticReviews(ctx, scope)
	if err != nil {
		return Inspection{}, err
	}
	notices, err := i.store.PendingReviewSourceChanges(ctx, scope)
	if err != nil {
		return Inspection{}, err
	}
	out := Inspection{Scope: scope, RecordID: recordID, LiveChecked: live, WriterConfigured: i.writerConfigured, Attempts: []AttemptStatus{}, Automatic: []IntentStatus{}, SourceInbox: []SourceNoticeStatus{}}
	type checked struct {
		status  string
		failure *core.FailureInfo
	}
	checks := make(map[string]checked)
	for _, attempt := range attempts {
		r := attempt.Request
		if r.LogicalID != scope+":"+r.Document.RecordID || (recordID != "" && r.Document.RecordID != recordID) {
			continue
		}
		status := AttemptStatus{DocumentID: r.Document.DocumentID, RecordID: r.Document.RecordID, Revision: r.Revision, Provider: r.Provider, State: attempt.State, Delivered: attempt.Delivered, Failure: attempt.Failure, Delivery: attempt.Delivery, RevisionStatus: RevisionNotChecked}
		if attempt.Submission != nil && attempt.Submission.Outcome != nil {
			status.Decision, status.ExternalID = attempt.Submission.Outcome.Decision, attempt.Submission.Outcome.ExternalID
		}
		if live {
			if scoped, ok := i.source.(ScopedSource); ok && scoped.ReviewLogicalID(r.Document.RecordID) != r.LogicalID {
				status.RevisionStatus = RevisionSourceMismatch
				status.NextAction = "inspect_source_binding"
				out.Attempts = append(out.Attempts, status)
				continue
			}
			key := r.Document.RecordID + "\x00" + r.Provider + "\x00" + r.ProviderVersion + "\x00" + r.RulesVersion
			// Cache the current preparation, not the comparison to an old
			// revision. Each saved revision can have a different validity.
			current, exists := checks[key]
			if !exists {
				pinned := Service{source: i.source, options: Options{Provider: r.Provider, ProviderVersion: r.ProviderVersion, RulesVersion: r.RulesVersion}}
				request, checkErr := pinned.Prepare(ctx, r.Document.RecordID)
				current = checked{status: request.Revision}
				if checkErr != nil {
					current.status = revisionErrorStatus(checkErr)
					if current.status == RevisionUnavailable {
						current.failure = failureInfo("source_check", preparationFailureCode(checkErr), checkErr)
					}
				}
				checks[key] = current
			}
			status.RevisionStatus = current.status
			status.CheckFailure = current.failure
			if current.failure == nil && current.status != RevisionNoAttachments && current.status != RevisionSourceRemoved {
				if current.status == r.Revision {
					status.RevisionStatus = RevisionCurrent
				} else {
					status.RevisionStatus = RevisionChanged
				}
			}
		}
		status.NextAction = nextAction(status, i.writerConfigured)
		out.Attempts = append(out.Attempts, status)
	}
	for _, intent := range intents {
		if intent.SourceScope == scope && (recordID == "" || intent.RecordID == recordID) {
			out.Automatic = append(out.Automatic, IntentStatus{intent.RecordID, intent.Generation, intent.NotBefore})
		}
	}
	for _, notice := range notices {
		if notice.Scope == scope {
			out.SourceInbox = append(out.SourceInbox, SourceNoticeStatus{notice.Kind, notice.Source, notice.RecordID, notice.EventID, notice.ReceivedAt})
		}
	}
	// A new pending detail may not have a snapshot yet. Check its ledger
	// readiness without invoking the configured audit implementation.
	if live && recordID != "" && len(out.Attempts) == 0 {
		if scoped, ok := i.source.(ScopedSource); ok && scoped.ReviewLogicalID(recordID) != scope+":"+recordID {
			return Inspection{}, fmt.Errorf("inspection source does not match scope")
		}
		probe := Service{source: i.source, options: Options{Provider: i.provider}}
		_, err := probe.Prepare(ctx, recordID)
		out.Readiness = "ready"
		if err != nil {
			out.Readiness = revisionErrorStatus(err)
			if out.Readiness == RevisionUnavailable {
				out.ReadinessFailure = failureInfo("source_check", preparationFailureCode(err), err)
			}
		}
	}
	sort.Slice(out.Attempts, func(a, b int) bool { return out.Attempts[a].DocumentID < out.Attempts[b].DocumentID })
	sort.Slice(out.Automatic, func(a, b int) bool { return out.Automatic[a].RecordID < out.Automatic[b].RecordID })
	return out, nil
}

func revisionErrorStatus(err error) string {
	if errors.Is(err, core.ErrNoAttachments) {
		return RevisionNoAttachments
	}
	if errors.Is(err, core.ErrSourceRemoved) {
		return RevisionSourceRemoved
	}
	return RevisionUnavailable
}

func preparationFailureCode(err error) string {
	if errors.Is(err, core.ErrLedgerIncomplete) {
		return "ledger_incomplete"
	}
	if errors.Is(err, core.ErrLedgerAmbiguous) {
		return "ledger_conflict"
	}
	return "source_unavailable"
}

func nextAction(s AttemptStatus, writerConfigured bool) string {
	if s.State == "submitting" || s.State == "unknown" {
		return "reconcile_provider"
	}
	if s.State == "failed" {
		return "resolve_rejection"
	}
	if s.RevisionStatus == RevisionUnavailable {
		return "restore_source_access"
	}
	if s.RevisionStatus == RevisionNoAttachments || s.RevisionStatus == RevisionSourceRemoved {
		return "source_inactive"
	}
	if s.RevisionStatus == RevisionChanged || (s.Delivery != nil && s.Delivery.State == "superseded" && s.RevisionStatus != RevisionCurrent) {
		return "submit_current_revision"
	}
	if s.State == "pending" {
		return "await_provider_result"
	}
	if s.State != "completed" || s.Decision == "" {
		return "inspect_state"
	}
	if s.Delivered {
		return "none"
	}
	if !writerConfigured {
		return "configure_writeback"
	}
	return "retry_writeback"
}
