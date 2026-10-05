package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type fileReviewSource struct{ *gateSource }

func (s *fileReviewSource) ReadDetail(ctx context.Context, id string) (review.Detail, error) {
	detail, err := s.gateSource.ReadDetail(ctx, id)
	if err == nil {
		detail.Files[0].Attachment.Name = "invoice.txt"
		detail.Files[0].Attachment.ContentType = "text/plain"
	}
	return detail, err
}
func filePreparedFixture(t *testing.T, target string) (*app.PreparedSource, *typedFields, *fileReviewSource, *state.Files, string) {
	t.Helper()
	source := &fileReviewSource{&gateSource{scope: "source", amount: "10"}}
	stored := &gateStore{}
	for _, id := range []string{"a", "b"} {
		request, err := appreview.PreparePinned(context.Background(), source, review.Request{LogicalID: source.ReviewLogicalID(id), Provider: "seal", ProviderVersion: "pin", Document: aggregate.Document{RecordID: id}})
		if err != nil {
			t.Fatal(err)
		}
		stored.attempts = append(stored.attempts, review.Attempt{Request: request, State: "completed", Submission: &review.Submission{DocumentID: request.Document.DocumentID, Status: "completed", Outcome: &review.Outcome{Decision: "review", Comment: "PRIVATE_COMMENT"}}})
	}
	gate, err := app.NewReviewGate(source, stored, "source", "seal", []string{"review"})
	if err != nil {
		t.Fatal(err)
	}
	fields := &typedFields{scope: "source"}
	fields.onRead = func(_ int, checks []app.InputCheck) []app.InputCheck {
		for n := range checks {
			checks[n].Issues = append(checks[n].Issues, app.InputIssue{Input: "files", Code: "approval_upload_required"})
			checks[n].Files = map[string][]app.SourceFile{"files": {{SourceScope: "source", SourceIdentity: "source-app", RecordID: checks[n].RecordID, FieldID: "attachment", ID: "file", Name: "invoice.txt", Size: int64(len("PRIVATE_BINARY"))}}}
		}
		return checks
	}
	root := t.TempDir()
	files, err := state.OpenFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := app.NewPreparedSourceWithUploads(fields, gate, files, target)
	if err != nil {
		t.Fatal(err)
	}
	return prepared, fields, source, files, root
}

func TestApprovalFilesUseOnlyReviewedBytesAndSavedTargetScopedReceipts(t *testing.T) {
	source, _, _, store, root := filePreparedFixture(t, "target")
	inspection, err := source.Inspect(context.Background(), []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspection.Rows(); !errors.Is(err, app.ErrInputsNotReady) {
		t.Fatal("Base attachment token became an approval code")
	}
	drafts, payloads, err := inspection.UploadDraft(false)
	if err != nil || len(drafts) != 2 || len(payloads) != 2 || string(payloads[0].Data) != "PRIVATE_BINARY" || payloads[0].Request.SourceIdentity != "source-app" {
		t.Fatal("reviewed file preparation lost its source or exact bytes")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatal("read-only preparation created upload state")
	}
	uploader := &countedUploader{}
	manager, _ := app.NewUploadManager(uploader, store)
	for _, payload := range payloads {
		if _, err := manager.Upload(context.Background(), payload, false); err != nil {
			t.Fatal(err)
		}
	}
	inspection, err = source.Inspect(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := inspection.Rows()
	if err != nil || rows[0].Fields["files"].References[0].ID != "approval-file-code" || rows[0].Fields["files"].Artifacts[0] != payloads[0].Request.ID || uploader.calls.Load() != 2 {
		t.Fatal("saved scoped upload was not resolved into the complete form")
	}
	opts := core.Options{SourceScope: "source", TargetScope: "target", Template: "template", ConfigurationVersion: "version", Submitter: core.Identity{Scope: "target", ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}}
	plans, err := core.BuildPlans(opts, rows)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Fields["files"] = core.Value{Kind: "files", References: rows[0].Fields["files"].References, Artifacts: []string{strings.Repeat("d", 64)}}
	changed, err := core.BuildPlans(opts, rows)
	if err != nil || changed[0].ID == plans[0].ID {
		t.Fatal("different upload provenance reused the old approval UUID")
	}
	raw, _ := json.Marshal(inspection)
	for _, private := range []string{"PRIVATE", "invoice.txt", "source-app", "approval-file-code"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private file metadata entered preview JSON")
		}
	}
}

func TestApprovalFilesHoldWholeSelectionForUnreviewedChangedOrUncertainFiles(t *testing.T) {
	for _, scenario := range []string{"unreviewed token", "foreign record", "foreign source", "changed name", "changed size", "bracket edit", "stale AI", "unknown", "rejected"} {
		t.Run(scenario, func(t *testing.T) {
			source, fields, reviews, store, _ := filePreparedFixture(t, "target")
			original := fields.onRead
			if scenario == "unknown" || scenario == "rejected" {
				inspection, _ := source.Inspect(context.Background(), []string{"a", "b"})
				_, payloads, err := inspection.UploadDraft(false)
				if err != nil {
					t.Fatal(err)
				}
				uploader := &countedUploader{err: errors.New("lost")}
				if scenario == "rejected" {
					uploader.err = core.ErrUploadRejected
				}
				manager, _ := app.NewUploadManager(uploader, store)
				_, _ = manager.Upload(context.Background(), payloads[0], false)
			} else if scenario == "stale AI" {
				reviews.amount = "20"
			} else {
				fields.onRead = func(call int, checks []app.InputCheck) []app.InputCheck {
					checks = original(call, checks)
					file := checks[0].Files["files"][0]
					switch scenario {
					case "unreviewed token":
						file.ID = "other-token"
					case "foreign record":
						file.RecordID = "foreign"
					case "foreign source":
						file.SourceScope = "other-source"
					case "changed name":
						file.Name = "other.txt"
					case "changed size":
						file.Size++
					case "bracket edit":
						if call == 2 {
							file.Name = "other.txt"
						}
					}
					checks[0].Files["files"][0] = file
					return checks
				}
			}
			inspection, err := source.Inspect(context.Background(), []string{"a", "b"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inspection.Rows(); !errors.Is(err, app.ErrInputsNotReady) {
				t.Fatal("one blocked file allowed a partial form")
			}
			_, _, err = inspection.UploadDraft(false)
			if err == nil {
				t.Fatal("file gate allowed an invalid upload draft")
			}
			_, _, err = inspection.UploadDraft(true)
			if (err == nil) != (scenario == "rejected") {
				t.Fatal("explicit retry bypassed an uncertain or unreviewed file")
			}
		})
	}
}

func TestApprovalUploadDraftRequiresActualPreparedPayloads(t *testing.T) {
	source, fields, _ := preparedSourceFixture(t)
	fields.onRead = func(_ int, checks []app.InputCheck) []app.InputCheck {
		checks[0].Issues = append(checks[0].Issues, app.InputIssue{Input: "files", Code: "approval_upload_required"})
		return checks
	}
	inspection, err := source.Inspect(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := inspection.UploadDraft(true); !errors.Is(err, app.ErrInputsNotReady) {
		t.Fatal("missing file resolver silently removed an upload requirement")
	}
}
