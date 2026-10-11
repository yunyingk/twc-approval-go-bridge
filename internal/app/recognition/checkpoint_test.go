package recognition_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type reader struct{}

func (reader) ReadAttachments(context.Context, string, string, string, string, map[string]bool) ([]invoice.Attachment, []string, error) {
	return []invoice.Attachment{{Name: "file", Data: []byte("image")}}, []string{"file"}, nil
}

type recognizer struct{ calls atomic.Int32 }

func (r *recognizer) Recognize(context.Context, invoice.Attachment) (invoice.Recognition, error) {
	r.calls.Add(1)
	return invoice.Recognition{Raw: json.RawMessage(`{"facts":{"total":"0"}}`), Facts: &invoice.Facts{Total: "0"}}, nil
}

func TestRestartResumesDeliveryWithoutAnotherOCR(t *testing.T) {
	store, err := state.NewFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &recognizer{}
	called := make(chan struct{}, 1)
	p, err := app.New(app.Config{BaseToken: "base", TableID: "table", FieldID: "field"}, reader{}, r, func(context.Context, app.Result) error { called <- struct{}{}; return errors.New("write failed") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.WithCheckpoints(store, "scope")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	if err := p.EnqueueRecord(ctx, "rec", map[string]bool{"file": true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("not processed")
	}
	cancel()
	<-done
	second, err := app.New(app.Config{BaseToken: "base", TableID: "table", FieldID: "field"}, reader{}, r, func(context.Context, app.Result) error { called <- struct{}{}; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	second.WithCheckpoints(store, "scope")
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { second.Run(ctx2); close(done2) }()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("pending task was not recovered")
	}
	// Wait for the durable acknowledgement rather than canceling between write and checkpoint.
	deadline := time.Now().Add(time.Second)
	for {
		task, err := store.LoadRecognition(context.Background(), "scope:rec:file")
		if err == nil && task.Delivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	cancel2()
	<-done2
	if r.calls.Load() != 1 {
		t.Fatal("paid OCR repeated across restart")
	}
}

func TestBaselineAndWorkerOwnershipSurviveReopen(t *testing.T) {
	root := t.TempDir()
	store, _ := state.NewFiles(root)
	ctx := context.Background()
	if _, _, err := store.RecognitionBaseline(ctx, "scope", map[string]bool{"rec:old": true}); err != nil {
		t.Fatal(err)
	}
	reopened, _ := state.NewFiles(root)
	baseline, found, err := reopened.RecognitionBaseline(ctx, "scope", nil)
	if err != nil || !found || !baseline["rec:old"] {
		t.Fatal("baseline lost")
	}
	lease, err := store.AcquireWorker("scope")
	if err != nil {
		t.Fatal(err)
	}
	if other, err := reopened.AcquireWorker("scope"); err == nil {
		other.Close()
		t.Fatal("second worker accepted")
	}
	lease.Close()
	other, err := reopened.AcquireWorker("scope")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

type failingRecognizer struct {
	calls atomic.Int32
}

func (f *failingRecognizer) Recognize(context.Context, invoice.Attachment) (invoice.Recognition, error) {
	f.calls.Add(1)
	return invoice.Recognition{}, errors.New("corrupted receipt file")
}

func TestFailedRecognitionStopsAtMaxAttempts(t *testing.T) {
	store, err := state.NewFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingRecognizer{}
	p, err := app.New(app.Config{BaseToken: "base", TableID: "table", FieldID: "field"}, reader{}, failing, func(context.Context, app.Result) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.WithCheckpoints(store, "scope")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Enqueue and run processor for 3 attempts
	for i := 0; i < app.DefaultMaxRecognitionAttempts; i++ {
		done := make(chan struct{})
		runCtx, runCancel := context.WithCancel(ctx)
		go func() {
			p.Run(runCtx)
			close(done)
		}()

		if err := p.EnqueueRecord(runCtx, "rec_fail", map[string]bool{"file": true}); err != nil {
			t.Fatal(err)
		}

		// Wait briefly for job to be processed and fail
		time.Sleep(100 * time.Millisecond)
		runCancel()
		<-done
	}

	if failing.calls.Load() != app.DefaultMaxRecognitionAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", app.DefaultMaxRecognitionAttempts, failing.calls.Load())
	}

	// Verify checkpoint is marked as Failed
	pending, err := store.PendingRecognition(ctx, "scope")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("failed task must not be returned in pending tasks, got %d", len(pending))
	}
}

