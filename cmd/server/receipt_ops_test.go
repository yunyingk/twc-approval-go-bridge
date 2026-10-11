package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func TestRunListFailedReceiptsAndRetryReceipt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Config{
		Runtime: config.RuntimeConfig{
			StateDir: dir,
		},
	}
	store, err := state.NewFiles(dir)
	if err != nil {
		t.Fatalf("failed to init state: %v", err)
	}

	// 1. Initial list: should be empty
	var buf bytes.Buffer
	if err := runListFailedReceipts(ctx, cfg, false, &buf); err != nil {
		t.Fatalf("runListFailedReceipts: %v", err)
	}
	if !strings.Contains(buf.String(), "暂无死信识别任务") {
		t.Errorf("expected empty message, got %s", buf.String())
	}

	// 2. Queue a failed task
	task := recognition.Checkpoint{
		Key:   "scope:rec-failed-1:token-1",
		Scope: "scope",
		Result: recognition.Result{
			RecordID:  "rec-failed-1",
			FileToken: "token-1",
		},
		Failed:    true,
		Attempts:  3,
		LastError: "receipt format not supported",
	}
	if err := store.QueueRecognition(ctx, task); err != nil {
		t.Fatalf("queue: %v", err)
	}

	// 3. List should show the task
	buf.Reset()
	if err := runListFailedReceipts(ctx, cfg, false, &buf); err != nil {
		t.Fatalf("runListFailedReceipts: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "rec-failed-1") || !strings.Contains(out, "receipt format not supported") {
		t.Errorf("expected failed task in table output, got %s", out)
	}

	// 4. Retry by recordID
	buf.Reset()
	if err := runRetryReceipt(ctx, cfg, "rec-failed-1", &buf); err != nil {
		t.Fatalf("runRetryReceipt: %v", err)
	}
	if !strings.Contains(buf.String(), "已成功重置 1 个识别任务") {
		t.Errorf("expected success retry message, got %s", buf.String())
	}

	// 5. List should be empty again
	buf.Reset()
	if err := runListFailedReceipts(ctx, cfg, false, &buf); err != nil {
		t.Fatalf("runListFailedReceipts: %v", err)
	}
	if !strings.Contains(buf.String(), "暂无死信识别任务") {
		t.Errorf("expected empty message after retry, got %s", buf.String())
	}
}
