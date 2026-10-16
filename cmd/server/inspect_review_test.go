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
	cfg := config.Config{
		Runtime: config.RuntimeConfig{StateDir: root},
		Business: &config.BusinessProfile{
			Tables: config.BusinessTables{
				ReimbursementDetails: config.TableBinding{BaseToken: "base", TableID: "details"},
			},
			Review: config.ReviewSettings{
				Provider: "model",
			},
		},
	}
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
	cfg.Runtime.StateDir = filepath.Join(root, "missing")
	if err := runReviewInspection(ctx, cfg, "all", false, &output); err == nil {
		t.Fatal("missing state accepted")
	}
	if _, err := os.Stat(cfg.Runtime.StateDir); !os.IsNotExist(err) {
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

func TestNewReviewService_WritebackControl(t *testing.T) {
	root := t.TempDir()
	boolFalse := false
	boolTrue := true

	baseCfg := func(wb *bool) config.Config {
		return config.Config{
			Runtime: config.RuntimeConfig{StateDir: root},
			Feishu: config.FeishuSettings{
				AppCredentials: config.AppCredentials{AppID: "cli_test", AppSecret: "sec_test"},
			},
			Seal: config.SealSettings{
				DocumentURL: "https://test.sealai.cc/api/v1/integrations/webhook/wh_test/document",
				BearerToken: "test_token",
			},
			Business: &config.BusinessProfile{
				Tables: config.BusinessTables{
					ReimbursementDetails: config.TableBinding{
						BaseToken: "base1",
						TableID:   "tbl_det",
						Fields:    map[string]string{"attachment": "fld_att", "detail_id": "fld_id"},
					},
					InvoiceLedger: config.TableBinding{
						BaseToken: "base1",
						TableID:   "tbl_lg",
						Fields:    map[string]string{"source_key": "fld_sk", "raw_json": "fld_raw", "invoice_number": "fld_inv_num"},
					},
				},
				Review: config.ReviewSettings{
					Provider:     "seal",
					Writeback:    wb,
					ResultFields: map[string]string{"decision": "fld_dec", "document_id": "fld_doc", "revision": "fld_rev"},
				},
			},
		}
	}

	// 1. Explicitly disabled via config (writeback = false)
	svc1, err := newReviewService(baseCfg(&boolFalse), false)
	if err != nil {
		t.Fatalf("newReviewService failed: %v", err)
	}
	if svc1.WriterConfigured() {
		t.Errorf("expected WriterConfigured to be false when config writeback=false")
	}

	// 2. Disabled via flag (disableWriteback = true)
	svc2, err := newReviewService(baseCfg(&boolTrue), true)
	if err != nil {
		t.Fatalf("newReviewService failed: %v", err)
	}
	if svc2.WriterConfigured() {
		t.Errorf("expected WriterConfigured to be false when disableWriteback=true flag is passed")
	}

	// 3. Enabled by default
	svc3, err := newReviewService(baseCfg(nil), false)
	if err != nil {
		t.Fatalf("newReviewService failed: %v", err)
	}
	if !svc3.WriterConfigured() {
		t.Errorf("expected WriterConfigured to be true by default when ResultFields are configured")
	}
}

