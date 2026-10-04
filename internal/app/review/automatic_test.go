package review_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

func TestAutomaticWaitsForCompleteLedgerAndRecoversAfterRestart(t *testing.T) {
	s := &source{amount: "10", missing: true}
	r := &reviewer{}
	svc, store := service(t, s, r, nil)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	auto, err := app.NewAutomatic(svc, store, "tenant:detail", logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	if r.calls != 0 || s.detailReads != 0 {
		t.Fatal("receipt delivery must enqueue without waiting for review")
	}
	if err := auto.ProcessPending(ctx); err == nil || r.calls != 0 {
		t.Fatal("incomplete invoice ledger reached the provider")
	}
	s.missing = false
	restarted, _ := app.NewAutomatic(svc, store, "tenant:detail", logger)
	if err := restarted.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.NotifyRecognition(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingAutomaticReviews(ctx, "tenant:detail")
	if err != nil || len(pending) != 0 || r.calls != 1 {
		t.Fatal("restart or duplicate delivery repeated the paid review")
	}
}

func TestAutomaticNotificationDuringSubmissionKeepsNewVersionPending(t *testing.T) {
	s := &source{amount: "10"}
	r := &reviewer{}
	svc, store := service(t, s, r, nil)
	auto, _ := app.NewAutomatic(svc, store, "scope", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	ctx := context.Background()
	r.before = func(core.Request) {
		s.amount = "20"
		if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
			t.Fatal(err)
		}
	}
	if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	if err := auto.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingAutomaticReviews(ctx, "scope")
	if err != nil || len(pending) != 1 || pending[0].Generation != 2 {
		t.Fatal("a receipt arriving during submission was discarded")
	}
	r.before = nil
	if err := auto.ProcessPending(ctx); err != nil || r.calls != 2 {
		t.Fatalf("new version was not submitted: calls=%d error=%v", r.calls, err)
	}
}

func TestAutomaticDoesNotRetryUncertainSubmission(t *testing.T) {
	r := &reviewer{fail: true}
	svc, store := service(t, &source{amount: "10"}, r, nil)
	auto, _ := app.NewAutomatic(svc, store, "scope", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	ctx := context.Background()
	if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := auto.ProcessPending(ctx); err == nil {
			t.Fatal("uncertain submission should stay pending for reconciliation")
		}
	}
	if r.calls != 1 {
		t.Fatal("uncertain submission was repeated")
	}
}

func TestAutomaticConcurrentNotificationsPreserveGenerationAndSource(t *testing.T) {
	r := &reviewer{}
	svc, store := service(t, &source{amount: "10"}, r, nil)
	auto, _ := app.NewAutomatic(svc, store, "scope", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	ctx := context.Background()
	if err := store.QueueAutomaticReview(ctx, "other-tenant", "other-record"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	pending, err := store.PendingAutomaticReviews(ctx, "scope")
	if err != nil || len(pending) != 1 || pending[0].Generation != 12 {
		t.Fatal("concurrent receipt notifications lost a generation")
	}
	if err := auto.ProcessPending(ctx); err != nil || r.calls != 1 {
		t.Fatalf("unexpected automatic review: calls=%d error=%v", r.calls, err)
	}
	other, err := store.PendingAutomaticReviews(ctx, "other-tenant")
	if err != nil || len(other) != 1 {
		t.Fatal("another source's intent was processed")
	}
}
