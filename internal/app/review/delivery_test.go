package review_test

import (
	"context"
	"errors"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func TestExplicitWritebackRecoversWithoutAnAuditProvider(t *testing.T) {
	ctx := context.Background()
	s, r, w := &source{amount: "10"}, &reviewer{}, &writer{}
	svc, store := service(t, s, r, w)
	result, err := svc.Submit(ctx, "rec")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := app.NewDelivery(s, store, w)
	if err != nil {
		t.Fatal(err)
	}
	reads := s.detailReads
	if _, err := delivery.Submit(ctx, "rec"); err == nil || s.detailReads != reads {
		t.Fatal("delivery service prepared a new billable submission")
	}
	if err := delivery.RetryWriteback(ctx, result.DocumentID); err == nil || w.calls != 0 {
		t.Fatal("pending review was written without an outcome")
	}
	s.detailErr = core.ErrLedgerIncomplete
	if err := delivery.Complete(ctx, result.DocumentID, "seal", core.Outcome{Decision: "review"}); err == nil {
		t.Fatal("unavailable source should delay delivery")
	}
	a, _ := store.ReadReview(ctx, result.DocumentID)
	if a.Failure == nil || a.Failure.Code != "ledger_incomplete" || a.Delivery == nil || a.Delivery.State != "check_failed" || a.Submission.Outcome == nil {
		t.Fatal("source check failure lost outcome or classification")
	}
	s.detailErr, w.fail = nil, true
	if err := delivery.RetryWriteback(ctx, result.DocumentID); err == nil {
		t.Fatal("write failure should remain retryable")
	}
	a, _ = store.ReadReview(ctx, result.DocumentID)
	if a.Failure == nil || a.Failure.Code != "writeback_failed" || a.Delivery.State != "write_failed" || a.Delivered {
		t.Fatal("write failure was marked delivered")
	}
	w.fail = false
	if err := delivery.RetryWriteback(ctx, result.DocumentID); err != nil {
		t.Fatal(err)
	}
	a, _ = store.ReadReview(ctx, result.DocumentID)
	if !a.Delivered || a.Failure != nil || a.Delivery.State != "delivered" || r.calls != 1 || w.calls != 2 {
		t.Fatal("recovery repeated provider call or retained obsolete failure")
	}
	reads = s.detailReads
	if err := delivery.RetryWriteback(ctx, result.DocumentID); err != nil || s.detailReads != reads || w.calls != 2 {
		t.Fatal("already delivered outcome was rewritten")
	}
	s.logicalID = "other-source:rec"
	if err := delivery.RetryWriteback(ctx, result.DocumentID); err == nil || s.detailReads != reads {
		t.Fatal("another source was accepted even for a delivered outcome")
	}
}

type archiveFailureStore struct {
	*state.Files
	updates int
	fail    bool
}

func (s *archiveFailureStore) Update(ctx context.Context, id string, change func(*core.Attempt) error) (core.Attempt, error) {
	s.updates++
	if s.fail && s.updates == 2 {
		return core.Attempt{}, errors.New("archive persistence unavailable")
	}
	return s.Files.Update(ctx, id, change)
}

func TestArchivePersistenceFailureRemainsRetryableAndExplicitRevertCanDeliver(t *testing.T) {
	ctx := context.Background()
	s, r, w := &source{amount: "10"}, &reviewer{}, &writer{}
	svc, files := service(t, s, r, w)
	result, err := svc.Submit(ctx, "rec")
	if err != nil {
		t.Fatal(err)
	}
	store := &archiveFailureStore{Files: files, fail: true}
	delivery, err := app.NewDelivery(s, store, w)
	if err != nil {
		t.Fatal(err)
	}
	s.amount = "20"
	err = delivery.Complete(ctx, result.DocumentID, "seal", core.Outcome{Decision: "approve"})
	if err == nil || errors.Is(err, app.ErrStale) {
		t.Fatal("failed archive would acknowledge callback or suppress recovery")
	}
	pending, err := files.PendingReviews(ctx)
	if err != nil || len(pending) != 1 || pending[0].Submission.Outcome == nil {
		t.Fatal("failed archive lost durable outcome or recovery eligibility")
	}
	store.fail = false
	if err := delivery.RetryWritebacks(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err = files.PendingReviews(ctx)
	if err != nil || len(pending) != 0 || w.calls != 0 {
		t.Fatal("superseded outcome remained in automatic recovery")
	}
	reads := s.detailReads
	if err := delivery.RetryWritebacks(ctx); err != nil || s.detailReads != reads {
		t.Fatal("archived outcome was repeatedly read")
	}
	s.amount = "10"
	if err := delivery.RetryWriteback(ctx, result.DocumentID); err != nil || r.calls != 1 || w.calls != 1 {
		t.Fatal("explicit recovery failed to restore the exact original revision")
	}
}
