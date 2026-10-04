package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestApprovalPreflightWithoutPolicyDoesNotCreateStateOrContactProviders(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{StateDir: root, FeishuAppID: "bridge", FeishuAppSecret: "secret", ReceiptBaseToken: "base", ReceiptTableID: "details", ReceiptFieldID: "attachment", ReceiptLedgerTableID: "ledger",
		ReceiptLedgerFieldIDs: map[string]string{"source_key": "source", "raw_json": "raw", "invoice_number": "number"}, ReviewProvider: "model"}
	before := stateContents(t, root)
	var output bytes.Buffer
	if err := runApprovalPreview(context.Background(), cfg, "b,a", &output); err != nil {
		t.Fatal(err)
	}
	var report approvalPreflight
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Kind != "preflight" || report.CreationAvailable || report.FormInputsChecked || len(report.Records) != 2 || report.Records[0].RecordID != "a" || report.Records[0].Code != "no_review" || report.Configuration[0] != "approval_not_configured" {
		t.Fatalf("incorrect approval preflight: %v", err)
	}
	if !reflect.DeepEqual(before, stateContents(t, root)) {
		t.Fatal("readiness command created state/lock files")
	}
	cfg.Business = &config.BusinessProfile{Approval: &config.ApprovalSettings{Mode: "manual", TargetIdentity: "approval", AllowedAIDecisions: []string{"review"}}}
	output.Reset()
	if err := runApprovalPreview(context.Background(), cfg, "a", &output); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(output.Bytes(), &report)
	if len(report.Configuration) != 2 || report.Configuration[0] != "target_credentials_missing" || report.Target != nil {
		t.Fatal("preflight fell back to a different target application")
	}
	cfg.StateDir = filepath.Join(root, "missing")
	if err := runApprovalPreview(context.Background(), cfg, "a", &output); err == nil {
		t.Fatal("missing readable state accepted")
	}
	if _, err := os.Stat(cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("preflight created missing state directory")
	}
}
