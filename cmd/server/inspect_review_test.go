package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func TestLocalReviewCommandNeedsNoProviderAndDoesNotCreateOrRewriteFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := config.Config{StateDir: root, ReceiptBaseToken: "base", ReceiptTableID: "details", ReviewProvider: "model"}
	store, err := state.NewFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{LogicalID: "feishu:base:details:rec", Provider: "seal", Revision: "old", Document: aggregate.Document{DocumentID: "saved", RecordID: "rec"}}
	if _, _, err := store.Begin(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAutomaticReview(ctx, "feishu:base:details", "rec"); err != nil {
		t.Fatal(err)
	}
	before := stateContents(t, root)
	var output bytes.Buffer
	if err := runReviewInspection(ctx, cfg, "all", false, &output); err != nil {
		t.Fatal(err)
	}
	var report app.Inspection
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.LiveChecked || len(report.Attempts) != 1 || len(report.Automatic) != 1 || report.Attempts[0].NextAction != "reconcile_provider" {
		t.Fatalf("local command report: %v", err)
	}
	if !reflect.DeepEqual(before, stateContents(t, root)) {
		t.Fatal("read-only command rewrote state or created locks")
	}
	cfg.StateDir = filepath.Join(root, "missing")
	if err := runReviewInspection(ctx, cfg, "all", false, &output); err == nil {
		t.Fatal("missing state accepted")
	}
	if _, err := os.Stat(cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("inspection created a state directory")
	}
}

func stateContents(t *testing.T, root string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(data)
	}
	return files
}
