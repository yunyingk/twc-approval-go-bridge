package review_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type source struct {
	amount  string
	missing bool
}

func (s *source) ReadDetail(context.Context, string) (core.Detail, error) {
	return core.Detail{DocumentID: "logical", DocumentSN: "SN", RecordID: "rec", StartTime: time.Now(), Files: []core.File{{Token: "file", Attachment: invoice.Attachment{Data: []byte("image")}}}}, nil
}
func (s *source) ReadLedgerEntry(context.Context, string) (core.LedgerEntry, error) {
	if s.missing {
		return core.LedgerEntry{}, errors.New("missing")
	}
	return core.LedgerEntry{RecordID: "ledger", Recognition: invoice.Recognition{Facts: &invoice.Facts{Total: s.amount}, Raw: json.RawMessage(`{"outputs":{}}`)}, Facts: dupcheck.Invoice{SourceKey: "rec:file"}}, nil
}
func (*source) FindInvoiceCandidates(context.Context, string) ([]dupcheck.Invoice, error) {
	return nil, nil
}

type reviewer struct {
	calls     int
	fail      bool
	completed bool
	before    func(core.Request)
}

func (r *reviewer) Review(_ context.Context, request core.Request) (core.Submission, error) {
	r.calls++
	if r.before != nil {
		r.before(request)
	}
	if r.fail {
		return core.Submission{}, errors.New("lost response")
	}
	s := core.Submission{DocumentID: request.Document.DocumentID, Status: "pending"}
	if r.completed {
		s.Status = "completed"
		s.Outcome = &core.Outcome{Decision: "review", Comment: "check"}
	}
	return s, nil
}

type writer struct {
	calls int
	fail  bool
}

func (w *writer) WriteReviewResult(context.Context, core.Request, core.Outcome) error {
	w.calls++
	if w.fail {
		return errors.New("offline")
	}
	return nil
}
func service(t *testing.T, s *source, r *reviewer, w *writer) (*app.Service, *state.Files) {
	t.Helper()
	store, err := state.NewFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var output app.Writer
	if w != nil {
		output = w
	}
	svc, err := app.New(s, r, app.Options{Provider: "seal", Versioned: true, Store: store, Writer: output})
	if err != nil {
		t.Fatal(err)
	}
	return svc, store
}

func TestSubmissionIsPersistentAndRevisionChanges(t *testing.T) {
	s := &source{amount: "10"}
	r := &reviewer{}
	svc, store := service(t, s, r, nil)
	ctx := context.Background()
	first, err := svc.Submit(ctx, "rec")
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := app.New(s, r, app.Options{Provider: "seal", Versioned: true, Store: store})
	second, err := restarted.Submit(ctx, "rec")
	if err != nil || first.DocumentID != second.DocumentID || r.calls != 1 {
		t.Fatalf("duplicate submission: %v %d", err, r.calls)
	}
	s.amount = "20"
	third, err := restarted.Submit(ctx, "rec")
	if err != nil || third.DocumentID == first.DocumentID || r.calls != 2 {
		t.Fatalf("missing new revision: %v", err)
	}
}
func TestUnknownSubmissionIsNotRetried(t *testing.T) {
	r := &reviewer{fail: true}
	svc, _ := service(t, &source{}, r, nil)
	for range 2 {
		if _, err := svc.Submit(context.Background(), "rec"); err == nil {
			t.Fatal("unknown attempt was accepted")
		}
	}
	if r.calls != 1 {
		t.Fatal("ambiguous request was resubmitted")
	}
}
func TestMissingLedgerStopsBeforeProvider(t *testing.T) {
	r := &reviewer{}
	svc, _ := service(t, &source{missing: true}, r, nil)
	if _, err := svc.Submit(context.Background(), "rec"); err == nil || r.calls != 0 {
		t.Fatal("partial data reached provider")
	}
}
func TestStaleCompletionIsStoredButNotWritten(t *testing.T) {
	s := &source{amount: "10"}
	r := &reviewer{}
	w := &writer{}
	svc, store := service(t, s, r, w)
	ctx := context.Background()
	result, err := svc.Submit(ctx, "rec")
	if err != nil {
		t.Fatal(err)
	}
	s.amount = "20"
	if err := svc.Complete(ctx, result.DocumentID, "seal", core.Outcome{Decision: "approve"}); !errors.Is(err, app.ErrStale) {
		t.Fatalf("stale result: %v", err)
	}
	attempt, err := store.Update(ctx, result.DocumentID, func(*core.Attempt) error { return nil })
	if err != nil || attempt.State != "completed" || attempt.Delivered || w.calls != 0 {
		t.Fatal("stale outcome was lost or applied")
	}
}
func TestWritebackRecoveryDoesNotRepeatModelAndRejectsConflict(t *testing.T) {
	r := &reviewer{completed: true}
	w := &writer{fail: true}
	svc, store := service(t, &source{}, r, w)
	ctx := context.Background()
	if _, err := svc.Submit(ctx, "rec"); err == nil {
		t.Fatal("writeback should fail")
	}
	w.fail = false
	if err := svc.RetryWritebacks(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Submit(ctx, "rec")
	if err != nil || r.calls != 1 || w.calls != 2 {
		t.Fatalf("unexpected retries: %v", err)
	}
	if err := svc.Complete(ctx, result.DocumentID, "seal", core.Outcome{Decision: "review", Comment: "check"}); err != nil {
		t.Fatal(err)
	}
	if w.calls != 2 {
		t.Fatal("duplicate callback was rewritten")
	}
	if err := svc.Complete(ctx, result.DocumentID, "seal", core.Outcome{Decision: "reject"}); err == nil {
		t.Fatal("conflicting result accepted")
	}
	if err := svc.Complete(ctx, result.DocumentID, "model", core.Outcome{Decision: "review"}); err == nil {
		t.Fatal("wrong provider accepted")
	}
	attempt, _ := store.Update(ctx, result.DocumentID, func(*core.Attempt) error { return nil })
	if !attempt.Delivered {
		t.Fatal("delivery not recorded")
	}
}
func TestCallbackBeforeSubmitResponseIsPreserved(t *testing.T) {
	r := &reviewer{}
	svc, _ := service(t, &source{}, r, nil)
	ctx := context.Background()
	r.before = func(request core.Request) {
		if err := svc.Complete(ctx, request.Document.DocumentID, "seal", core.Outcome{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := svc.Submit(ctx, "rec")
	if err != nil || result.Status != "completed" || result.Outcome.Decision != "approve" {
		t.Fatalf("early result overwritten: %#v %v", result, err)
	}
}
