package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type gateSource struct {
	scope, amount string
	reads         int
	failure       error
	payments      *core.TransactionEvidence
	candidates    []dupcheck.Invoice
}

func (s *gateSource) ReviewLogicalID(id string) string { return s.scope + ":" + id }
func (s *gateSource) ReadDetail(_ context.Context, id string) (core.Detail, error) {
	s.reads++
	if s.failure != nil {
		return core.Detail{}, s.failure
	}
	return core.Detail{DocumentID: s.ReviewLogicalID(id), DocumentSN: id, RecordID: id, StartTime: time.Now(),
		Files:   []core.File{{Token: "file", Attachment: invoice.Attachment{Data: []byte("PRIVATE_BINARY")}}},
		Context: map[string]string{"reason": "PRIVATE_CONTEXT"}, Transactions: s.payments}, nil
}
func (s *gateSource) ReadLedgerEntry(_ context.Context, key string) (core.LedgerEntry, error) {
	return core.LedgerEntry{RecordID: "ledger", Recognition: invoice.Recognition{Facts: &invoice.Facts{Total: s.amount, Currency: "USD"}, Raw: json.RawMessage(`{"PRIVATE_RAW":true}`)},
		Facts: dupcheck.Invoice{SourceKey: key, Number: "invoice"}}, nil
}
func (s *gateSource) FindInvoiceCandidates(context.Context, string) ([]dupcheck.Invoice, error) {
	return s.candidates, nil
}

type gateStore struct {
	attempts []core.Attempt
	reads    int
}

func (s *gateStore) ReviewAttempts(context.Context, string) ([]core.Attempt, error) {
	s.reads++
	return s.attempts, nil
}

func gateAttempt(t *testing.T, s *gateSource, id, pin, state, decision string) core.Attempt {
	t.Helper()
	seed := core.Request{LogicalID: s.ReviewLogicalID(id), Provider: "seal", ProviderVersion: pin, Document: aggregate.Document{RecordID: id}}
	request, err := appreview.PreparePinned(context.Background(), s, seed)
	if err != nil {
		t.Fatal(err)
	}
	a := core.Attempt{Request: request, State: state, Delivered: true}
	if state == "completed" {
		a.Submission = &core.Submission{DocumentID: request.Document.DocumentID, Status: state, Outcome: &core.Outcome{Decision: decision, Comment: "PRIVATE_COMMENT", URL: "PRIVATE_URL"}}
	}
	return a
}
func gateCheck(t *testing.T, s *gateSource, store *gateStore, allowed []string, ids ...string) []app.ReviewCheck {
	t.Helper()
	gate, err := app.NewReviewGate(s, store, "source", "seal", allowed)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := gate.Check(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	return checks
}

func TestApprovalGateUsesCurrentEvidenceAndNeverSerializesPrivateSnapshot(t *testing.T) {
	s := &gateSource{scope: "source", amount: "9007199254740993.12"}
	store := &gateStore{attempts: []core.Attempt{gateAttempt(t, s, "b", "PRIVATE_PIN", "completed", "review"), gateAttempt(t, s, "a", "PRIVATE_PIN", "completed", "review")}}
	before, _ := json.Marshal(store.attempts)
	checks := gateCheck(t, s, store, []string{"review"}, "b", "a")
	if store.reads != 1 || len(checks) != 2 || checks[0].RecordID != "a" || checks[0].Code != "current" || checks[1].Evidence == nil {
		t.Fatal("current selection was not checked against saved source versions")
	}
	if checks[0].Evidence.Current.Document.Invoices[0].Recognition.Facts.Total != s.amount || checks[0].Evidence.Reference.CurrentRevision != checks[0].Revision {
		t.Fatal("current exact facts were lost before form preparation")
	}
	encoded, _ := json.Marshal(checks)
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("private snapshot/pin/comment entered diagnostic JSON")
	}
	after, _ := json.Marshal(store.attempts)
	if string(before) != string(after) {
		t.Fatal("readiness gate changed stored results")
	}
}

func TestApprovalGateDoesNotTreatDeliveredResultsAsCurrent(t *testing.T) {
	for _, change := range []string{"amount", "payments", "candidates"} {
		t.Run(change, func(t *testing.T) {
			s := &gateSource{scope: "source", amount: "10"}
			store := &gateStore{attempts: []core.Attempt{gateAttempt(t, s, "a", "pin", "completed", "approve")}}
			switch change {
			case "amount":
				s.amount = "20"
			case "payments":
				s.payments = &core.TransactionEvidence{Issues: []core.EvidenceIssue{{Code: "missing_transaction_relation"}}}
			case "candidates":
				s.candidates = []dupcheck.Invoice{{RecordID: "new-candidate", Number: "invoice"}}
			}
			check := gateCheck(t, s, store, []string{"approve"}, "a")[0]
			if check.Code != "review_stale" || check.CurrentFacts || check.Evidence != nil {
				t.Fatal("delivered stale result passed approval gate")
			}
		})
	}
}

func TestApprovalGateCachesPreparationButComparesEverySavedRevision(t *testing.T) {
	s := &gateSource{scope: "source", amount: "10"}
	old := gateAttempt(t, s, "a", "pin", "completed", "approve")
	s.amount = "20"
	current := gateAttempt(t, s, "a", "pin", "pending", "")
	store := &gateStore{attempts: []core.Attempt{old, current}}
	reads := s.reads
	check := gateCheck(t, s, store, []string{"approve"}, "a")[0]
	if check.Code != "review_pending" || !check.CurrentFacts || check.Evidence != nil || s.reads != reads+1 {
		t.Fatal("old outcome bypassed current pending result, or reads repeated for one pin")
	}
	other := gateAttempt(t, s, "a", "different-pin", "completed", "approve")
	store.attempts = []core.Attempt{current, other}
	check = gateCheck(t, s, store, []string{"approve"}, "a")[0]
	if check.Code != "review_conflict" || check.Evidence != nil {
		t.Fatal("two current pins were silently resolved by ordering")
	}
}

func TestApprovalGateRequiresPolicyAndKeepsSourceFailuresDistinct(t *testing.T) {
	s := &gateSource{scope: "source", amount: "10"}
	store := &gateStore{attempts: []core.Attempt{gateAttempt(t, s, "a", "pin", "completed", "reject")}}
	for _, policy := range []struct {
		allowed []string
		code    string
	}{{nil, "policy_not_configured"}, {[]string{"approve"}, "decision_not_allowed"}, {[]string{"reject"}, "current"}} {
		check := gateCheck(t, s, store, policy.allowed, "a")[0]
		if check.Code != policy.code || !check.CurrentFacts || (check.Evidence != nil) != (policy.code == "current") {
			t.Fatal("explicit AI decision policy was bypassed")
		}
	}
	for _, failure := range []struct {
		err  error
		code string
	}{{core.ErrSourceRemoved, "source_removed"}, {core.ErrNoAttachments, "no_attachments"}, {core.ErrLedgerIncomplete, "ledger_incomplete"}, {core.ErrLedgerAmbiguous, "ledger_conflict"}, {errors.New("PRIVATE_PERMISSION_FAILURE"), "source_unavailable"}} {
		s.failure = failure.err
		check := gateCheck(t, s, store, []string{"reject"}, "a")[0]
		if check.Code != failure.code || check.Evidence != nil {
			t.Fatal("source failure was accepted or hidden")
		}
	}
}

func TestApprovalGateRejectsForeignAndMalformedStateBeforeReadingSource(t *testing.T) {
	s := &gateSource{scope: "source", amount: "10"}
	a := gateAttempt(t, s, "a", "pin", "completed", "approve")
	a.Request.Document.DocumentID = "foreign"
	store := &gateStore{attempts: []core.Attempt{a}}
	reads := s.reads
	if check := gateCheck(t, s, store, []string{"approve"}, "a")[0]; check.Code != "review_state_invalid" || s.reads != reads {
		t.Fatal("malformed saved identity reached source reads")
	}
	s.scope = "other-source"
	gate, _ := app.NewReviewGate(s, store, "source", "seal", []string{"approve"})
	if _, err := gate.Check(context.Background(), []string{"a"}); err == nil || s.reads != reads || store.reads != 1 {
		t.Fatal("foreign source was queried")
	}
}
