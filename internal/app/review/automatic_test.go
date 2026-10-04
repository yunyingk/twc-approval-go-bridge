package review_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

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

func TestAutomaticChangesPersistDeadlineCoalesceAndDedupe(t *testing.T) {
	s, r := &source{amount: "10"}, &reviewer{}
	svc, store := service(t, s, r, nil)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	auto, _ := app.NewAutomatic(svc, store, "scope", logger)
	ctx := context.Background()
	deadline := time.Now().Add(time.Hour)
	if err := store.QueueAutomaticReviewChange(ctx, "scope", "rec", "future", deadline); err != nil {
		t.Fatal(err)
	}
	if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	if err := auto.ProcessPending(ctx); err != nil || r.calls != 0 || s.detailReads != 0 {
		t.Fatal("recognition delivery bypassed an edit quiet period")
	}
	restarted, _ := app.NewAutomatic(svc, store, "scope", logger)
	if err := restarted.ProcessPending(ctx); err != nil || r.calls != 0 {
		t.Fatal("restart lost edit deadline")
	}
	intents, _ := store.PendingAutomaticReviews(ctx, "scope")
	if len(intents) != 1 || !intents[0].NotBefore.Equal(deadline) {
		t.Fatal("deadline was not persisted")
	}
	if err := store.AcknowledgeAutomaticReview(ctx, intents[0]); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"first", "second", "second"} {
		if err := store.QueueAutomaticReviewChange(ctx, "scope", "rec", event, time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	intents, _ = store.PendingAutomaticReviews(ctx, "scope")
	if len(intents) != 1 || intents[0].Generation != 4 {
		t.Fatal("duplicate edit advanced generation")
	}
	if err := restarted.ProcessPending(ctx); err != nil || r.calls != 1 {
		t.Fatalf("edits did not coalesce: %v", err)
	}
	if err := store.QueueAutomaticReviewChange(ctx, "scope", "rec", "second", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	intents, _ = store.PendingAutomaticReviews(ctx, "scope")
	if len(intents) != 0 {
		t.Fatal("redelivered acknowledged edit reopened intent")
	}
	if err := store.QueueAutomaticReviewChange(ctx, "scope", "rec", "no-fact-change", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ProcessPending(ctx); err != nil || r.calls != 1 {
		t.Fatal("same revision repeated paid review")
	}
	s.amount = "20"
	if err := store.QueueAutomaticReviewChange(ctx, "scope", "rec", "new-facts", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ProcessPending(ctx); err != nil || r.calls != 2 {
		t.Fatal("changed facts did not get a new revision")
	}
}

func TestAutomaticChangeDuringSubmissionSurvivesAcknowledgement(t *testing.T) {
	s, r := &source{amount: "10"}, &reviewer{}
	svc, store := service(t, s, r, nil)
	auto, _ := app.NewAutomatic(svc, store, "scope", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err := auto.EnableChanges(time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r.before = func(core.Request) {
		s.amount = "20"
		if err := auto.NotifyChange(ctx, "rec", "during-submit"); err != nil {
			t.Fatal(err)
		}
	}
	if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
		t.Fatal(err)
	}
	if err := auto.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	intents, _ := store.PendingAutomaticReviews(ctx, "scope")
	if len(intents) != 1 || intents[0].Generation != 2 {
		t.Fatal("edit during review was lost")
	}
	r.before = nil
	if err := auto.ProcessPending(ctx); err != nil || r.calls != 2 {
		t.Fatalf("new edit not submitted: %v", err)
	}
}

func TestAutomaticInactiveSourcesStopButFailuresRemainPending(t *testing.T) {
	cases := []struct {
		name     string
		source   *source
		terminal bool
	}{
		{"empty attachments", &source{noAttachments: true}, true},
		{"removed record", &source{detailErr: core.ErrSourceRemoved}, true},
		{"permission failure", &source{detailErr: errors.New("forbidden")}, false},
		{"incomplete ledger", &source{missing: true}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			r := &reviewer{}
			svc, store := service(t, test.source, r, nil)
			auto, _ := app.NewAutomatic(svc, store, "scope", slog.New(slog.NewJSONHandler(io.Discard, nil)))
			ctx := context.Background()
			if err := auto.NotifyRecognition(ctx, "rec"); err != nil {
				t.Fatal(err)
			}
			err := auto.ProcessPending(ctx)
			if (err == nil) != test.terminal || r.calls != 0 {
				t.Fatalf("unexpected source handling: %v", err)
			}
			pending, _ := store.PendingAutomaticReviews(ctx, "scope")
			if (len(pending) == 0) != test.terminal {
				t.Fatal("wrong terminal/pending state")
			}
		})
	}
}

func TestAutomaticRunWakesWhenEditDeadlineExpires(t *testing.T) {
	r := &reviewer{}
	svc, store := service(t, &source{amount: "10"}, r, nil)
	auto, _ := app.NewAutomatic(svc, store, "scope", slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err := auto.EnableChanges(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	r.before = func(core.Request) { cancel() }
	if err := auto.NotifyChange(ctx, "rec", "edit"); err != nil {
		t.Fatal(err)
	}
	go func() { defer close(done); auto.Run(ctx) }()
	<-done
	if r.calls != 1 {
		t.Fatal("edit deadline waited for the ordinary 30-second recovery poll")
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
