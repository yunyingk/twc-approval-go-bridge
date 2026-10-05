package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type commandRequestRenderer struct{ calls, failAt int }

func (*commandRequestRenderer) TargetScope() string { return "feishu-app:bridge" }
func (r *commandRequestRenderer) PrepareRequest(_ context.Context, plan core.Plan) (core.RequestArtifact, error) {
	r.calls++
	if r.calls == r.failAt {
		return core.RequestArtifact{}, errors.New("PRIVATE_RENDER_FAILURE")
	}
	body, _ := json.Marshal(struct{ UUID, FileCode, Amount string }{plan.ID, plan.Rows[0].Fields["files"].References[0].ID, plan.Rows[0].Fields["amount"].Decimal})
	return core.NewRequestArtifact("test.native.request.v1", body)
}
func TestRequestPreparationCommandPersistsPrivateRequestsAndOnlyPrintsSummary(t *testing.T) {
	cfg, source, _, _, store, uploader, _ := preparationFixture(t)
	inspection, err := source.Inspect(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	_, files, err := inspection.UploadDraft(false)
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := app.NewUploadManager(uploader, store)
	for _, file := range files {
		if _, err := manager.Upload(context.Background(), file, false); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	renderer := &commandRequestRenderer{}
	if err := prepareApprovalRequests(context.Background(), cfg, []string{"b", "a"}, source, renderer, approvalPreviewTarget{Scope: "feishu-app:bridge", Template: "template", Version: "version"}, store, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "PRIVATE") || strings.Contains(output.String(), "9007199254740993") {
		t.Fatal("private request or facts entered stdout")
	}
	var report approvalPreparationReport
	json.Unmarshal(output.Bytes(), &report)
	if !report.Complete || report.CreationAvailable || len(report.Plans) != 2 || renderer.calls != 4 || uploader.calls != 2 || report.AuditFile == "" {
		t.Fatal("complete grouped private requests missing or preparation uploaded again")
	}
	saved, err := store.ReadApprovalPreparation(context.Background(), report.ID)
	if err != nil || saved.Validate() != nil {
		t.Fatal("reported audit not readable or valid")
	}
	raw, err := os.ReadFile(report.AuditFile)
	if err != nil || !strings.Contains(string(raw), "PRIVATE_CODE_") {
		t.Fatal("full native request missing from private audit")
	}
	if attempts, err := store.ApprovalAttempts(context.Background(), "feishu:base:details"); err != nil || len(attempts) != 0 {
		t.Fatal("preparation created or reserved an instance")
	}
}
func TestRequestPreparationCommandHoldsMissingFileAndFailedFinalGroup(t *testing.T) {
	for _, scenario := range []string{"missing files", "last group fails"} {
		t.Run(scenario, func(t *testing.T) {
			cfg, source, _, _, store, uploader, _ := preparationFixture(t)
			renderer := &commandRequestRenderer{}
			if scenario == "last group fails" {
				inspection, _ := source.Inspect(context.Background(), []string{"a", "b"})
				_, files, err := inspection.UploadDraft(false)
				if err != nil {
					t.Fatal(err)
				}
				manager, _ := app.NewUploadManager(uploader, store)
				for _, file := range files {
					if _, err := manager.Upload(context.Background(), file, false); err != nil {
						t.Fatal(err)
					}
				}
				renderer.failAt = 2
			}
			var output bytes.Buffer
			if err := prepareApprovalRequests(context.Background(), cfg, []string{"a", "b"}, source, renderer, approvalPreviewTarget{Scope: "feishu-app:bridge", Template: "template", Version: "version"}, store, &output); err != nil {
				t.Fatal(err)
			}
			var report approvalPreparationReport
			json.Unmarshal(output.Bytes(), &report)
			if report.Complete || report.ID != "" || len(report.Plans) != 0 || len(report.Issues) != 1 || strings.Contains(output.String(), "PRIVATE") {
				t.Fatal("partial request exposed as a prepared batch")
			}
		})
	}
}
func TestRequestPreparationCommandRejectsUnconfiguredAndInvalidSelectionsBeforeIO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	for _, selection := range []string{"record", "", "a,a", "a, b"} {
		if err := runApprovalRequestPreparation(context.Background(), config.Config{StateDir: root}, selection, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid or unconfigured preparation command ran")
		}
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unconfigured preparation wrote state")
	}
}
