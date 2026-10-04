package review_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type statusFailure struct{}

func (statusFailure) Error() string   { return "PRIVATE_UPSTREAM_ERROR" }
func (statusFailure) HTTPStatus() int { return 409 }

func TestInspectionSafeFailureMetadataAndLocalReadWithoutWrites(t *testing.T) {
	s := &source{amount: "10", logicalID: "scope:rec"}
	r := &reviewer{failure: errors.Join(core.ErrRequestRejected, statusFailure{})}
	svc, store := service(t, s, r, nil)
	ctx := context.Background()
	request, err := svc.Prepare(ctx, "rec")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, "rec"); err == nil {
		t.Fatal("rejection missing")
	}
	attempt, err := store.ReadReview(ctx, request.Document.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.State != "failed" || attempt.Failure == nil || attempt.Failure.Code != "request_rejected" || attempt.Failure.HTTPStatus != 409 || attempt.Failure.OccurredAt.IsZero() {
		t.Fatal("rejection metadata not retained")
	}
	if _, err := store.Update(ctx, request.Document.DocumentID, func(a *core.Attempt) error {
		a.Request.ProviderVersion = "PRIVATE_PROVIDER_URL"
		a.Request.Context = map[string]string{"private": "PRIVATE_BUSINESS_FACT"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAutomaticReview(ctx, "scope", "rec"); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueReviewSourceChange(ctx, app.SourceChange{Scope: "scope", Source: "ledger", Kind: app.InvoiceSource, RecordID: "invoice", EventID: "event", InvoiceNumbers: []string{"PRIVATE_INVOICE_NUMBER"}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadReview(ctx, request.Document.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	reads := s.detailReads
	inspector, err := app.NewInspector(nil, store, true, "model")
	if err != nil {
		t.Fatal(err)
	}
	report, err := inspector.Inspect(ctx, "scope", "rec", false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(report)
	for _, private := range []string{"PRIVATE_UPSTREAM_ERROR", "PRIVATE_PROVIDER_URL", "PRIVATE_BUSINESS_FACT", "PRIVATE_INVOICE_NUMBER"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("inspection disclosed %s", private)
		}
	}
	if len(report.Attempts) != 1 || report.Attempts[0].NextAction != "resolve_rejection" || report.Attempts[0].RevisionStatus != app.RevisionNotChecked || len(report.Automatic) != 1 || len(report.SourceInbox) != 1 || s.detailReads != reads || r.calls != 1 {
		t.Fatal("local inspection mutated or contacted an external system")
	}
	after, err := store.ReadReview(ctx, request.Document.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(before)
	b2, _ := json.Marshal(after)
	if string(b1) != string(b2) {
		t.Fatal("inspection modified saved attempt")
	}
}

func TestLiveInspectionChecksEachRevisionWithOnePinnedPreparation(t *testing.T) {
	s := &source{amount: "10", logicalID: "scope:rec"}
	r := &reviewer{}
	svc, store := service(t, s, r, nil)
	ctx := context.Background()
	if _, err := svc.Submit(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	s.amount = "20"
	if _, err := svc.Submit(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	reads := s.detailReads
	inspector, _ := app.NewInspector(s, store, true, "model")
	report, err := inspector.Inspect(ctx, "scope", "rec", true)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, attempt := range report.Attempts {
		counts[attempt.RevisionStatus]++
	}
	if len(report.Attempts) != 2 || counts[app.RevisionCurrent] != 1 || counts[app.RevisionChanged] != 1 || s.detailReads != reads+1 || r.calls != 2 {
		t.Fatal("live inspection reused a comparison instead of current evidence, repeated reads, or called reviewer")
	}
	// A mismatched adapter is rejected before touching another tenant's rows.
	s.logicalID = "different-scope:rec"
	reads = s.detailReads
	report, err = inspector.Inspect(ctx, "scope", "rec", true)
	if err != nil || s.detailReads != reads {
		t.Fatal("live inspection read another configured tenant")
	}
	for _, attempt := range report.Attempts {
		if attempt.RevisionStatus != app.RevisionSourceMismatch {
			t.Fatal("source mismatch was hidden")
		}
	}
}

func TestLiveInspectionExposesInactiveSourcesAndMissingLedgerReadiness(t *testing.T) {
	s := &source{amount: "10", logicalID: "scope:rec"}
	svc, store := service(t, s, &reviewer{}, nil)
	ctx := context.Background()
	if _, err := svc.Submit(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	inspector, _ := app.NewInspector(s, store, true, "seal")
	s.noAttachments = true
	report, err := inspector.Inspect(ctx, "scope", "rec", true)
	if err != nil || report.Attempts[0].RevisionStatus != app.RevisionNoAttachments || report.Attempts[0].NextAction != "source_inactive" {
		t.Fatal("empty source looked approved/current")
	}
	s.noAttachments = false
	s.detailErr = core.ErrSourceRemoved
	report, err = inspector.Inspect(ctx, "scope", "rec", true)
	if err != nil || report.Attempts[0].RevisionStatus != app.RevisionSourceRemoved {
		t.Fatal("removed record looked current")
	}
	s.detailErr = core.ErrLedgerIncomplete
	report, err = inspector.Inspect(ctx, "scope", "rec", true)
	if err != nil || report.Attempts[0].CheckFailure == nil || report.Attempts[0].CheckFailure.Code != "ledger_incomplete" {
		t.Fatal("source failure classification lost")
	}
	// First recognition may still be waiting before an attempt exists.
	_, fresh := service(t, s, &reviewer{}, nil)
	probe, _ := app.NewInspector(s, fresh, true, "model")
	report, err = probe.Inspect(ctx, "scope", "rec", true)
	if err != nil || report.Readiness != app.RevisionUnavailable || report.ReadinessFailure == nil || report.ReadinessFailure.Code != "ledger_incomplete" {
		t.Fatal("first-review readiness unavailable")
	}
}

func TestUnknownResultStaysReconciliationRequiredAfterInspection(t *testing.T) {
	s := &source{amount: "10", logicalID: "scope:rec"}
	r := &reviewer{fail: true}
	svc, store := service(t, s, r, nil)
	ctx := context.Background()
	if _, err := svc.Submit(ctx, "rec"); err == nil {
		t.Fatal("unknown result expected")
	}
	s.amount = "20"
	inspector, _ := app.NewInspector(s, store, true, "seal")
	report, err := inspector.Inspect(ctx, "scope", "rec", true)
	if err != nil || report.Attempts[0].State != "unknown" || report.Attempts[0].NextAction != "reconcile_provider" || report.Attempts[0].Failure == nil || report.Attempts[0].Failure.Code != "result_unknown" || r.calls != 1 {
		t.Fatal("inspection enabled blind retry or hid uncertainty")
	}
	if len(report.Automatic) != 0 {
		t.Fatal("inspection queued a new review")
	}
}
