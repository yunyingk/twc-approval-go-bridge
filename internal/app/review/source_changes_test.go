package review_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func dependencyAttempt(scope, id, version, ledgerID, number string, candidates ...dupcheck.Invoice) core.Attempt {
	return core.Attempt{Request: core.Request{LogicalID: scope + ":" + id, Document: aggregate.Document{DocumentID: scope + ":" + id + ":" + version, RecordID: id,
		Invoices: []aggregate.Invoice{{LedgerRecordID: ledgerID, Facts: dupcheck.Invoice{Number: number}, Candidates: candidates}}}}}
}

func TestSourceDependenciesCoverOwnInvoicesOldAndNewCandidatesAndPayments(t *testing.T) {
	a := dependencyAttempt("scope", "a", "v1", "own-a", "INV-A", dupcheck.Invoice{RecordID: "candidate"})
	b := dependencyAttempt("scope", "b", "v1", "own-b", "INV-B")
	b.Request.Transactions = &core.TransactionEvidence{Source: "payments", LinkedRecordIDs: []string{"payment"}}
	c := dependencyAttempt("scope", "c", "v1", "own-c", "INV-C")
	c.Request.Transactions = &core.TransactionEvidence{Source: "another-payment-table", LinkedRecordIDs: []string{"payment"}}
	d := dependencyAttempt("other-tenant", "d", "v1", "own-a", "INV-A")
	attempts := []core.Attempt{a, a, b, c, d}
	cases := []struct {
		name   string
		change app.SourceChange
		want   []string
	}{
		{"own correction", app.SourceChange{Scope: "scope", Kind: app.InvoiceSource, RecordID: "own-a"}, []string{"a"}},
		{"candidate removed", app.SourceChange{Scope: "scope", Kind: app.InvoiceSource, RecordID: "candidate"}, []string{"a"}},
		{"new or renumbered candidate", app.SourceChange{Scope: "scope", Kind: app.InvoiceSource, RecordID: "new", InvoiceNumbers: []string{"INV-A", "INV-B"}}, []string{"a", "b"}},
		{"unknown hints widen current scope", app.SourceChange{Scope: "scope", Kind: app.InvoiceSource, AllDetails: true}, []string{"a", "b", "c"}},
		{"payment changed or removed", app.SourceChange{Scope: "scope", Source: "payments", Kind: app.TransactionSource, RecordID: "payment"}, []string{"b"}},
		{"unrelated row", app.SourceChange{Scope: "scope", Kind: app.InvoiceSource, RecordID: "unused", InvoiceNumbers: []string{"UNRELATED"}}, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := app.AffectedDetails(test.change, attempts)
			if len(got) != len(test.want) {
				t.Fatalf("got %v want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("got %v want %v", got, test.want)
				}
			}
		})
	}
}

type durableScheduler struct {
	store  *state.Files
	scope  string
	failID string
}

func (s *durableScheduler) NotifyChangeAt(ctx context.Context, id, eventID string, receivedAt time.Time) error {
	if id == s.failID {
		return errors.New("queue unavailable")
	}
	return s.store.QueueAutomaticReviewChange(ctx, s.scope, id, eventID, receivedAt.Add(10*time.Second))
}

func TestSourceChangesRecoverPartialFanoutAndKeepEventTimeAndScope(t *testing.T) {
	ctx := context.Background()
	store, err := state.NewFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		a := dependencyAttempt("scope", id, "v1", "own-"+id, "SAME")
		if _, _, err := store.Begin(ctx, a.Request); err != nil {
			t.Fatal(err)
		}
	}
	other := dependencyAttempt("other-tenant", "other", "v1", "other-own", "SAME")
	if _, _, err := store.Begin(ctx, other.Request); err != nil {
		t.Fatal(err)
	}
	target := &durableScheduler{store: store, scope: "scope", failID: "b"}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	flow, err := app.NewSourceChanges("scope", map[app.SourceKind]string{app.InvoiceSource: "ledger"}, store, target, logger)
	if err != nil {
		t.Fatal(err)
	}
	change := app.SourceChange{Scope: "scope", Source: "ledger", Kind: app.InvoiceSource, RecordID: "new-candidate", EventID: "event", InvoiceNumbers: []string{"SAME", "SAME"}}
	if err := flow.EnqueueSourceChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	// The inbox must not be decoded as an AutomaticIntent even though both
	// are pending state records for one process.
	if pending, err := store.PendingAutomaticReviews(ctx, "scope"); err != nil || len(pending) != 0 {
		t.Fatal("source row became a detail submission")
	}
	if err := flow.ProcessPending(ctx); err == nil {
		t.Fatal("partial fanout was acknowledged")
	}
	inbox, err := store.PendingReviewSourceChanges(ctx, "scope")
	if err != nil || len(inbox) != 1 {
		t.Fatal("failed fanout lost its inbox")
	}
	deadline := inbox[0].ReceivedAt.Add(10 * time.Second)
	intents, _ := store.PendingAutomaticReviews(ctx, "scope")
	if len(intents) != 1 || intents[0].RecordID != "a" || intents[0].Generation != 1 || !intents[0].NotBefore.Equal(deadline) {
		t.Fatal("first fanout intent or deadline lost")
	}
	target.failID = ""
	restarted, _ := app.NewSourceChanges("scope", map[app.SourceKind]string{app.InvoiceSource: "ledger"}, store, target, logger)
	if err := restarted.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	intents, _ = store.PendingAutomaticReviews(ctx, "scope")
	if len(intents) != 2 {
		t.Fatal("restart did not finish fanout")
	}
	for _, intent := range intents {
		if intent.Generation != 1 || !intent.NotBefore.Equal(deadline) {
			t.Fatal("fanout retry extended quiet period or repeated event")
		}
	}
	if err := restarted.EnqueueSourceChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	inbox, _ = store.PendingReviewSourceChanges(ctx, "scope")
	if len(inbox) != 0 {
		t.Fatal("redelivered completed event reopened the inbox")
	}
	foreign, _ := store.PendingAutomaticReviews(ctx, "other-tenant")
	if len(foreign) != 0 {
		t.Fatal("fanout touched another tenant")
	}
	conflict := change
	conflict.AllDetails = true
	if err := flow.EnqueueSourceChange(ctx, conflict); err == nil {
		t.Fatal("conflicting event replacement accepted")
	}
	change.EventID = "new-binding-event"
	if err := flow.EnqueueSourceChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	switched, _ := app.NewSourceChanges("scope", map[app.SourceKind]string{app.InvoiceSource: "new-ledger"}, store, target, logger)
	if err := switched.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	inbox, _ = store.PendingReviewSourceChanges(ctx, "scope")
	if len(inbox) != 1 {
		t.Fatal("a changed source binding redirected or discarded old notice")
	}
}

func TestSourceChangeRereadsCurrentFactsAndReusesReviewAttempt(t *testing.T) {
	s := &source{amount: "10", logicalID: "scope:rec", transactions: &core.TransactionEvidence{Source: "payments", LinkedRecordIDs: []string{"payment"}, Transactions: []core.Transaction{{RecordID: "payment", OriginalAmount: "10"}}}}
	r := &reviewer{}
	svc, store := service(t, s, r, nil)
	ctx := context.Background()
	if _, err := svc.Submit(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	auto, _ := app.NewAutomatic(svc, store, "scope", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err := auto.EnableChanges(time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	flow, err := app.NewSourceChanges("scope", map[app.SourceKind]string{app.TransactionSource: "payments"}, store, auto, nil)
	if err != nil {
		t.Fatal(err)
	}
	change := app.SourceChange{Scope: "scope", Source: "payments", Kind: app.TransactionSource, RecordID: "payment", EventID: "same-facts"}
	if err := flow.EnqueueSourceChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	if err := flow.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := auto.ProcessPending(ctx); err != nil || r.calls != 1 {
		t.Fatalf("unchanged source repeated review: %v", err)
	}
	s.transactions.Transactions[0].OriginalAmount = "12"
	change.EventID = "new-facts"
	if err := flow.EnqueueSourceChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	if err := flow.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := auto.ProcessPending(ctx); err != nil || r.calls != 2 {
		t.Fatalf("changed payment did not produce current revision: %v", err)
	}
}

func TestSourceChangeKeepsFirstReviewWithoutSavedDependenciesPending(t *testing.T) {
	store, err := state.NewFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.QueueAutomaticReview(ctx, "scope", "first-review"); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAutomaticReview(ctx, "another-tenant", "foreign"); err != nil {
		t.Fatal(err)
	}
	target := &durableScheduler{store: store, scope: "scope"}
	flow, err := app.NewSourceChanges("scope", map[app.SourceKind]string{app.TransactionSource: "payments"}, store, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := flow.EnqueueSourceChange(ctx, app.SourceChange{Scope: "scope", Source: "payments", Kind: app.TransactionSource, RecordID: "payment", EventID: "edit-before-snapshot"}); err != nil {
		t.Fatal(err)
	}
	if err := flow.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingAutomaticReviews(ctx, "scope")
	if err != nil || len(pending) != 1 || pending[0].RecordID != "first-review" || pending[0].Generation != 2 {
		t.Fatal("source change was dropped before the first snapshot existed")
	}
	foreign, _ := store.PendingAutomaticReviews(ctx, "another-tenant")
	if len(foreign) != 1 || foreign[0].Generation != 1 {
		t.Fatal("conservative fanout crossed tenant scope")
	}
}
