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
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type preparationReviewSource struct {
	planReviewSource
	stale bool
}

func (s *preparationReviewSource) ReadDetail(ctx context.Context, id string) (review.Detail, error) {
	detail, err := s.planReviewSource.ReadDetail(ctx, id)
	detail.Files[0].Attachment.Name = "PRIVATE_INVOICE.txt"
	detail.Files[0].Attachment.ContentType = "text/plain"
	if s.stale {
		detail.Context = map[string]string{"reason": "new fact"}
	}
	return detail, err
}

type preparationFields struct {
	planFields
	reads  int
	onRead func(int, []app.InputCheck)
}

func (s *preparationFields) ReadApprovalInputs(ctx context.Context, ids []string) ([]app.InputCheck, error) {
	checks, err := s.planFields.ReadApprovalInputs(ctx, ids)
	s.reads++
	for n := range checks {
		checks[n].Issues = append(checks[n].Issues, app.InputIssue{Input: "files", Code: "approval_upload_required"})
		checks[n].Files = map[string][]app.SourceFile{"files": {{SourceScope: s.ApprovalSourceScope(), SourceIdentity: "feishu-app:bridge", RecordID: checks[n].RecordID, FieldID: "attachment", ID: "file", Name: "PRIVATE_INVOICE.txt", Size: int64(len("PRIVATE_BYTES"))}}}
	}
	if s.onRead != nil {
		s.onRead(s.reads, checks)
	}
	return checks, err
}

type preparationValidator struct{ drafts, forms, failAt int }

func (v *preparationValidator) ValidateUploadDraft(_ context.Context, version string, rows []core.Row, payloads []app.UploadPayload) error {
	v.drafts++
	if version != "version" || len(rows) != 1 || len(payloads) != 1 || payloads[0].Request.RecordID != rows[0].RecordID || v.drafts == v.failAt {
		return errors.New("PRIVATE_INVALID_FORM")
	}
	return nil
}
func (v *preparationValidator) ValidatePlan(_ context.Context, plan core.Plan) error {
	v.forms++
	if plan.Validate() != nil || len(plan.Rows[0].Fields["files"].Artifacts) != 1 {
		return errors.New("invalid complete form")
	}
	return nil
}

type preparationUploader struct {
	calls    int
	failAt   int
	rejected bool
	onCall   func(int)
}

func (*preparationUploader) TargetScope() string { return "feishu-app:bridge" }
func (u *preparationUploader) Upload(_ context.Context, request core.UploadRequest, _ []byte) (core.Identity, error) {
	u.calls++
	if u.onCall != nil {
		u.onCall(u.calls)
	}
	if u.calls == u.failAt {
		if u.rejected {
			return core.Identity{}, core.ErrUploadRejected
		}
		return core.Identity{}, errors.New("PRIVATE_LOST_UPLOAD")
	}
	return core.Identity{Scope: u.TargetScope(), ID: "PRIVATE_CODE_" + request.RecordID}, nil
}

func preparationFixture(t *testing.T) (config.Config, *app.PreparedSource, *preparationFields, *preparationReviewSource, *state.Files, *preparationUploader, *preparationValidator) {
	t.Helper()
	reviews := &preparationReviewSource{}
	stored := &planReviewStore{}
	for _, id := range []string{"a", "b"} {
		request, err := appreview.PreparePinned(context.Background(), reviews, review.Request{LogicalID: reviews.ReviewLogicalID(id), Provider: "seal", ProviderVersion: "pin", Document: aggregate.Document{RecordID: id}})
		if err != nil {
			t.Fatal(err)
		}
		stored.attempts = append(stored.attempts, review.Attempt{Request: request, State: "completed", Submission: &review.Submission{DocumentID: request.Document.DocumentID, Status: "completed", Outcome: &review.Outcome{Decision: "review"}}})
	}
	gate, err := app.NewReviewGate(reviews, stored, "feishu:base:details", "seal", []string{"review"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fields := &preparationFields{planFields: planFields{split: true}}
	source, err := app.NewPreparedSourceWithUploads(fields, gate, store, "feishu-app:bridge")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ReceiptBaseToken: "base", ReceiptTableID: "details", Business: &config.BusinessProfile{Approval: &config.ApprovalSettings{Mode: "manual", SubmitterOpenID: "ou_submitter", AllowedAIDecisions: []string{"review"}, GroupBy: []config.ApprovalGroup{{Axis: "project"}}}}}
	return cfg, source, fields, reviews, store, &preparationUploader{}, &preparationValidator{}
}
func preparationRun(t *testing.T, cfg config.Config, source *app.PreparedSource, manager *app.UploadManager, validator *preparationValidator, retry bool) approvalFileReport {
	t.Helper()
	var output bytes.Buffer
	if err := prepareApprovalFiles(context.Background(), cfg, []string{"b", "a"}, retry, source, manager, validator, approvalPreviewTarget{Scope: "feishu-app:bridge", Template: "template", Version: "version"}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "PRIVATE") {
		t.Fatal("file preparation output exposed values, file codes, filename or raw error")
	}
	var report approvalFileReport
	if json.Unmarshal(output.Bytes(), &report) != nil {
		t.Fatal("invalid preparation report")
	}
	return report
}

func TestFilePreparationValidatesEveryGroupBeforeUploadAndReusesSavedReceipts(t *testing.T) {
	cfg, source, _, _, store, uploader, validator := preparationFixture(t)
	uploader.onCall = func(int) {
		if validator.drafts != 2 {
			t.Fatal("file POST preceded full grouped validation")
		}
	}
	manager, _ := app.NewUploadManager(uploader, store)
	report := preparationRun(t, cfg, source, manager, validator, false)
	if !report.Complete || !report.FormValidated || report.CreationAvailable || len(report.Plans) != 2 || uploader.calls != 2 || validator.forms != 2 {
		t.Fatal("complete files and validated plans were not prepared")
	}
	report = preparationRun(t, cfg, source, manager, validator, false)
	if !report.Complete || uploader.calls != 2 || len(report.Uploads) != 2 || report.Uploads[0].Runs != 1 {
		t.Fatal("saved receipts were uploaded again")
	}
	if pending, err := store.ApprovalAttempts(context.Background(), "feishu:base:details"); err != nil || len(pending) != 0 {
		t.Fatal("file preparation reserved members or created an approval attempt")
	}
}

func TestFilePreparationHoldsEntireSelectionBeforeAnyUpload(t *testing.T) {
	for _, scenario := range []string{"second group invalid", "missing group", "stale AI"} {
		t.Run(scenario, func(t *testing.T) {
			cfg, source, fields, reviews, store, uploader, validator := preparationFixture(t)
			switch scenario {
			case "second group invalid":
				validator.failAt = 2
			case "missing group":
				fields.onRead = func(_ int, checks []app.InputCheck) { delete(checks[0].Row.GroupValues, "project") }
			case "stale AI":
				reviews.stale = true
			}
			manager, _ := app.NewUploadManager(uploader, store)
			report := preparationRun(t, cfg, source, manager, validator, false)
			if report.Complete || uploader.calls != 0 || len(report.Issues) != 1 {
				t.Fatal("incomplete selection caused a file POST")
			}
		})
	}
}

func TestFilePreparationRetainsPartialReceiptsAndNeverResendsUnknown(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "rejected"}[rejected], func(t *testing.T) {
			cfg, source, _, _, store, uploader, validator := preparationFixture(t)
			uploader.failAt = 2
			uploader.rejected = rejected
			manager, _ := app.NewUploadManager(uploader, store)
			report := preparationRun(t, cfg, source, manager, validator, false)
			if report.Complete || len(report.Uploads) != 2 || report.Uploads[0].State != "uploaded" || uploader.calls != 2 {
				t.Fatal("partial upload receipts lost")
			}
			uploader.failAt = 0
			report = preparationRun(t, cfg, source, manager, validator, false)
			if report.Complete || uploader.calls != 2 {
				t.Fatal("ordinary rerun resent a rejected or uncertain file")
			}
			report = preparationRun(t, cfg, source, manager, validator, true)
			if report.Complete != rejected || (rejected && uploader.calls != 3) || (!rejected && uploader.calls != 2) {
				t.Fatal("explicit retry lost saved file or bypassed uncertainty")
			}
			if rejected && report.Uploads[1].Runs != 2 {
				t.Fatal("retry erased rejected upload history")
			}
		})
	}
}

func TestFilePreparationRechecksCurrentFactsAfterUpload(t *testing.T) {
	cfg, source, _, reviews, store, uploader, validator := preparationFixture(t)
	uploader.onCall = func(call int) {
		if call == 2 {
			reviews.stale = true
		}
	}
	manager, _ := app.NewUploadManager(uploader, store)
	report := preparationRun(t, cfg, source, manager, validator, false)
	if report.Complete || report.FormValidated || len(report.Plans) != 0 || len(report.Uploads) != 2 || report.Reviews[0].Code != "review_stale" {
		t.Fatal("source edit during upload exposed a usable plan")
	}
}

func TestFilePreparationRequiresManualConfigBeforeStateOrNetwork(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	for _, cfg := range []config.Config{{StateDir: root}, {StateDir: root, Business: &config.BusinessProfile{Approval: &config.ApprovalSettings{Mode: "disabled"}}}} {
		if err := runApprovalFilePreparation(context.Background(), cfg, "record", false, &bytes.Buffer{}); err == nil {
			t.Fatal("unconfigured upload command ran")
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unconfigured command created state")
		}
	}
}
