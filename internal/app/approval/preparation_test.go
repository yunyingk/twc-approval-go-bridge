package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type requestRenderer struct {
	calls     int
	failAt    int
	changedAt int
	after     func(int)
}

func (*requestRenderer) TargetScope() string { return "target" }
func (r *requestRenderer) PrepareRequest(_ context.Context, plan core.Plan) (core.RequestArtifact, error) {
	r.calls++
	if r.after != nil {
		r.after(r.calls)
	}
	if r.calls == r.failAt {
		return core.RequestArtifact{}, errors.New("PRIVATE_FORM_ERROR")
	}
	text := "PRIVATE_FORM & exact"
	if r.calls >= r.changedAt && r.changedAt > 0 {
		text = "different request"
	}
	body, _ := json.Marshal(struct {
		ID, Text string
		Number   json.RawMessage
	}{plan.ID, text, json.RawMessage("9007199254740993.12")})
	return core.NewRequestArtifact("test.adapter.request.v1", body)
}
func requestPreparerFixture(t *testing.T) (*app.RequestPreparer, *typedFields, *gateSource, *requestRenderer, *state.Files, string) {
	t.Helper()
	source, fields, reviews := preparedSourceFixture(t)
	fields.onRead = func(_ int, checks []app.InputCheck) []app.InputCheck {
		for n := range checks {
			checks[n].Row.GroupValues["project"] = checks[n].RecordID
		}
		return checks
	}
	root := t.TempDir()
	store, err := state.OpenFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	renderer := &requestRenderer{}
	preparer, err := app.NewRequestPreparer(source, renderer, store, core.Options{SourceScope: "source", TargetScope: "target", Template: "template", ConfigurationVersion: "version", Submitter: core.Identity{Scope: "target", ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}})
	if err != nil {
		t.Fatal(err)
	}
	return preparer, fields, reviews, renderer, store, root
}
func TestApprovalPreparationSavesWholeCurrentBatchWithPrivateProofAndStableDigest(t *testing.T) {
	preparer, _, _, renderer, store, root := requestPreparerFixture(t)
	result, err := preparer.Prepare(context.Background(), []string{"b", "a"})
	if err != nil || result.Batch == nil || result.Issue != "" || renderer.calls != 4 || len(result.Batch.Plans) != 2 || result.Batch.Validate() != nil {
		t.Fatalf("request preparation not complete: %v / %s", err, result.Issue)
	}
	for _, proof := range result.Batch.Reviews {
		if !proof.Snapshot.Document.StartTime.IsZero() || len(proof.Snapshot.Document.Invoices[0].Attachment.Data) != 0 || proof.Snapshot.Document.Invoices[0].Attachment.URL != "" || proof.Snapshot.Document.Invoices[0].Recognition.Facts.Total != "9007199254740993.12" || proof.Outcome.Comment != "PRIVATE_COMMENT" || proof.Snapshot.ProviderVersion != "pin" {
			t.Fatal("audit lost checked facts/pins or persisted original content")
		}
	}
	path, err := store.ApprovalPreparationPath(result.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 || !strings.Contains(string(before), "9007199254740993.12") || !strings.Contains(string(before), "PRIVATE_FORM") {
		t.Fatal("private audit missing exact request or restrictive permissions")
	}
	again, err := preparer.Prepare(context.Background(), []string{"a", "b"})
	if err != nil || again.Batch == nil || again.Batch.ID != result.Batch.ID || again.Batch.PreparedAt != result.Batch.PreparedAt {
		t.Fatal("fetch time/order caused another audit or overwrote history")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("repeated preparation rewrote the audit")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("private audit escaped through default diagnostics")
	}
	if attempts, err := store.ApprovalAttempts(context.Background(), "source"); err != nil || len(attempts) != 0 {
		t.Fatal("preparation reserved members")
	}
	if pending, err := store.PendingReviews(context.Background()); err != nil || len(pending) != 0 {
		t.Fatal("prepared audit entered AI delivery scanner")
	}
	if attempts, err := store.ReviewAttempts(context.Background(), "source"); err != nil || len(attempts) != 0 {
		t.Fatal("prepared audit entered AI readiness history")
	}
	if len(mustReadDirectory(t, root)) != 2 {
		t.Fatal("unexpected state files from preparation")
	}
}
func TestApprovalPreparationHoldsWholeBatchForAnyInvalidOrChangingMember(t *testing.T) {
	for _, scenario := range []string{"last group fails", "stale AI", "form edit", "request edit", "AI edit during rendering"} {
		t.Run(scenario, func(t *testing.T) {
			preparer, fields, reviews, renderer, _, root := requestPreparerFixture(t)
			switch scenario {
			case "last group fails":
				renderer.failAt = 2
			case "stale AI":
				reviews.amount = "1"
			case "form edit":
				previous := fields.onRead
				fields.onRead = func(call int, checks []app.InputCheck) []app.InputCheck {
					checks = previous(call, checks)
					if call >= 3 {
						checks[0].Row.Fields["amount"] = core.Value{Kind: "money", Decimal: "1", Currency: "USD"}
					}
					return checks
				}
			case "request edit":
				renderer.changedAt = 3
			case "AI edit during rendering":
				renderer.after = func(call int) {
					if call == 2 {
						reviews.amount = "1"
					}
				}
			}
			result, err := preparer.Prepare(context.Background(), []string{"a", "b"})
			if err != nil || result.Batch != nil || result.Issue == "" || len(mustReadDirectory(t, root)) != 0 {
				t.Fatal("partial or changing selection persisted a usable request")
			}
		})
	}
}
func TestApprovalPreparationConcurrentStoreReuseAndCorruptFilesStayClosed(t *testing.T) {
	preparer, _, _, _, store, root := requestPreparerFixture(t)
	result, err := preparer.Prepare(context.Background(), []string{"a", "b"})
	if err != nil || result.Batch == nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for n := 0; n < 12; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			other, err := state.OpenFiles(root)
			if err != nil {
				t.Error(err)
				return
			}
			batch := *result.Batch
			batch.PreparedAt = time.Now().UTC()
			saved, err := other.SaveApprovalPreparation(context.Background(), batch)
			if err != nil || saved.PreparedAt != result.Batch.PreparedAt {
				t.Error("concurrent save replaced original preparation")
			}
		}()
	}
	workers.Wait()
	path, _ := store.ApprovalPreparationPath(result.Batch.ID)
	for _, raw := range []string{"", "{}", "bad-json"} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadApprovalPreparation(context.Background(), result.Batch.ID); err == nil || errors.Is(err, core.ErrPreparationUnknown) {
			t.Fatal("existing corrupt audit treated as absent")
		}
		if _, err := store.SaveApprovalPreparation(context.Background(), *result.Batch); err == nil {
			t.Fatal("corrupt audit overwritten")
		}
	}
}
func TestApprovalPreparationRejectsEditedFactsRequestsAndMemberProofs(t *testing.T) {
	preparer, _, _, _, _, _ := requestPreparerFixture(t)
	result, err := preparer.Prepare(context.Background(), []string{"a", "b"})
	if err != nil || result.Batch == nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result.Batch)
	for _, scenario := range []string{"body", "source facts", "AI outcome", "file bytes", "file URL", "missing proof", "foreign proof", "digest"} {
		t.Run(scenario, func(t *testing.T) {
			var batch core.PreparedBatch
			json.Unmarshal(raw, &batch)
			switch scenario {
			case "body":
				batch.Plans[0].Request.Body = json.RawMessage(`{"changed":true}`)
			case "source facts":
				batch.Reviews[0].Snapshot.Document.Invoices[0].Recognition.Facts.Total = "2"
			case "AI outcome":
				batch.Reviews[0].Outcome.Decision = "approve"
			case "file bytes":
				batch.Reviews[0].Snapshot.Document.Invoices[0].Attachment.Data = []byte("original bytes")
			case "file URL":
				batch.Reviews[0].Snapshot.Document.Invoices[0].Attachment.URL = "temporary-url"
			case "missing proof":
				batch.Reviews = batch.Reviews[:1]
			case "foreign proof":
				batch.Reviews[0].Snapshot.LogicalID = "foreign"
			case "digest":
				batch.ID = strings.Repeat("a", 64)
			}
			if batch.Validate() == nil {
				t.Fatal("edited preparation was accepted")
			}
		})
	}
}

type failingPreparationStore struct {
	inner       *state.Files
	afterCommit bool
}

func (s *failingPreparationStore) SaveApprovalPreparation(ctx context.Context, batch core.PreparedBatch) (core.PreparedBatch, error) {
	if s.afterCommit {
		if _, err := s.inner.SaveApprovalPreparation(ctx, batch); err != nil {
			return core.PreparedBatch{}, err
		}
	}
	return batch, errors.New("PRIVATE_SAVE_FAILURE")
}

func TestApprovalPreparationSaveFailureNeverReportsCompleteOrReservesMembers(t *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "before save", true: "after atomic save"}[afterCommit], func(t *testing.T) {
			source, _, _ := preparedSourceFixture(t)
			root := t.TempDir()
			store, _ := state.OpenFiles(root)
			preparer, err := app.NewRequestPreparer(source, &requestRenderer{}, &failingPreparationStore{store, afterCommit}, core.Options{
				SourceScope: "source", TargetScope: "target", Template: "template", ConfigurationVersion: "version",
				Submitter: core.Identity{Scope: "target", ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := preparer.Prepare(context.Background(), []string{"a", "b"})
			if err == nil || result.Batch != nil || result.Issue != "preparation_save_failed" {
				t.Fatal("failed persistence reported a usable complete batch")
			}
			if attempts, err := store.ApprovalAttempts(context.Background(), "source"); err != nil || len(attempts) != 0 {
				t.Fatal("failed audit save reserved source members")
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("save failure exposed the private request")
			}
			if afterCommit {
				// A repeated prepare can safely reuse an atomic save whose caller
				// saw an error. It must not create an instance or another audit.
				preparer, _ = app.NewRequestPreparer(source, &requestRenderer{}, store, core.Options{
					SourceScope: "source", TargetScope: "target", Template: "template", ConfigurationVersion: "version",
					Submitter: core.Identity{Scope: "target", ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}})
				result, err = preparer.Prepare(context.Background(), []string{"a", "b"})
				if err != nil || result.Batch == nil || len(mustReadDirectory(t, root)) != 2 {
					t.Fatal("saved audit could not be reused after persistence uncertainty")
				}
			} else if len(mustReadDirectory(t, root)) != 0 {
				t.Fatal("pre-save failure unexpectedly wrote state")
			}
		})
	}
}
