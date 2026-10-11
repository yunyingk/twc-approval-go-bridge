package state_test

import (
	"context"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func TestRecognition_QueueSavePendingFailedAndRetry(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := state.NewFiles(dir)
	if err != nil {
		t.Fatalf("failed to create state files: %v", err)
	}

	scope := "test-scope"
	task1 := recognition.Checkpoint{
		Key:   "key-1",
		Scope: scope,
		Result: recognition.Result{
			RecordID:  "rec-1",
			FileToken: "file-token-1",
		},
	}
	task2 := recognition.Checkpoint{
		Key:   "key-2",
		Scope: scope,
		Result: recognition.Result{
			RecordID:  "rec-2",
			FileToken: "file-token-2",
		},
	}

	// 1. Queue both tasks
	if err := store.QueueRecognition(ctx, task1); err != nil {
		t.Fatalf("queue task1: %v", err)
	}
	if err := store.QueueRecognition(ctx, task2); err != nil {
		t.Fatalf("queue task2: %v", err)
	}

	// 2. Both should be pending
	pending, err := store.PendingRecognition(ctx, scope)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending tasks, got %d", len(pending))
	}

	// 3. Mark task1 as failed
	task1.Failed = true
	task1.Attempts = 3
	task1.LastError = "model error: rate limited"
	if err := store.SaveRecognition(ctx, task1); err != nil {
		t.Fatalf("save task1: %v", err)
	}

	// 4. Now pending should only have task2
	pending, err = store.PendingRecognition(ctx, scope)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].Key != "key-2" {
		t.Fatalf("expected 1 pending task (key-2), got %v", pending)
	}

	// 5. Failed should list task1
	failed, err := store.FailedRecognition(ctx, scope)
	if err != nil {
		t.Fatalf("failed recognition: %v", err)
	}
	if len(failed) != 1 || failed[0].Key != "key-1" {
		t.Fatalf("expected 1 failed task (key-1), got %v", failed)
	}
	if failed[0].LastError != "model error: rate limited" {
		t.Errorf("expected last error 'model error: rate limited', got %q", failed[0].LastError)
	}

	// 6. Retry task1 by RecordID
	retriedCount, err := store.RetryRecognition(ctx, "rec-1")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retriedCount != 1 {
		t.Fatalf("expected 1 task retried, got %d", retriedCount)
	}

	// 7. Verify task1 is no longer failed and is pending again
	failed, err = store.FailedRecognition(ctx, scope)
	if err != nil {
		t.Fatalf("failed recognition after retry: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("expected 0 failed tasks after retry, got %d", len(failed))
	}

	pending, err = store.PendingRecognition(ctx, scope)
	if err != nil {
		t.Fatalf("pending after retry: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending tasks after retry, got %d", len(pending))
	}
}
