package approval

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type nativeBatchEvidence struct{ attempts []review.Attempt }

func (*nativeBatchEvidence) ReviewLogicalID(id string) string { return "source:" + id }
func (s *nativeBatchEvidence) ReadDetail(_ context.Context, id string) (review.Detail, error) {
	return review.Detail{DocumentID: s.ReviewLogicalID(id), DocumentSN: id, RecordID: id, StartTime: time.Now(),
		Files: []review.File{{Token: "file", Attachment: invoice.Attachment{Name: "invoice.pdf", ContentType: "application/pdf", Data: []byte("PRIVATE_ORIGINAL")}}}}, nil
}
func (*nativeBatchEvidence) ReadLedgerEntry(_ context.Context, key string) (review.LedgerEntry, error) {
	return review.LedgerEntry{RecordID: "ledger-" + key, Recognition: invoice.Recognition{Raw: json.RawMessage(`{"amount":9007199254740993.12}`), Facts: &invoice.Facts{Total: "9007199254740993.12", Currency: "USD"}}, Facts: dupcheck.Invoice{SourceKey: key}}, nil
}
func (*nativeBatchEvidence) FindInvoiceCandidates(context.Context, string) ([]dupcheck.Invoice, error) {
	return nil, nil
}
func (s *nativeBatchEvidence) ReviewAttempts(context.Context, string) ([]review.Attempt, error) {
	return s.attempts, nil
}

type nativeBatchFields struct{ source *gatewaySource }

func (*nativeBatchFields) ApprovalSourceScope() string { return "source" }
func (f *nativeBatchFields) ReadApprovalInputs(ctx context.Context, ids []string) ([]app.InputCheck, error) {
	rows, err := f.source.ReadApprovalRows(ctx, ids)
	checks := []app.InputCheck{}
	for _, row := range rows {
		row.Review = core.ReviewRef{}
		row.SourceVersion = strings.Repeat("b", 64)
		checks = append(checks, app.InputCheck{RecordID: row.RecordID, Row: row, Issues: []app.InputIssue{}})
	}
	return checks, err
}

func TestNativeBatchSendsAuditedSDKBodiesAndQueriesOriginalUUIDWithoutTemplate(t *testing.T) {
	ctx := context.Background()
	store, _ := state.NewFiles(t.TempDir())
	var batch core.PreparedBatch
	posts, gets := 0, 0
	var firstUUID string
	gateway, rows, definition := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "POST":
			posts++
			raw, _ := io.ReadAll(req.Body)
			var body struct {
				UUID string `json:"uuid"`
			}
			if json.Unmarshal(raw, &body) != nil {
				t.Fatal("invalid native JSON")
			}
			found := false
			for _, prepared := range batch.Plans {
				if prepared.Plan.ID == body.UUID {
					found = string(raw) == string(prepared.Request.Body)
					attempt, err := store.ReadApproval(ctx, "source", body.UUID)
					if err != nil || attempt.Phase != "submitting" || attempt.Audit == nil || *attempt.Audit != prepared.AuditReference(batch.ID) {
						t.Fatal("native POST preceded its exact durable audit/send intent")
					}
				}
			}
			all, _ := store.ApprovalAttempts(ctx, "source")
			if !found || len(all) != 2 {
				t.Fatal("native request differed from its audit or batch not fully reserved")
			}
			if posts == 1 {
				firstUUID = body.UUID
				return nil, errors.New("PRIVATE_LOST_RESPONSE")
			}
			return definitionResponse(`{"code":0,"data":{"instance_code":"second-instance"}}`), nil
		case "GET":
			gets++
			if req.URL.Path != "/open-apis/approval/v4/instances/"+firstUUID || req.URL.Query().Get("user_id_type") != "open_id" {
				t.Fatal("recovery queried another instance, identity or unsent UUID")
			}
			return definitionResponse(`{"code":0,"data":{"instance_code":"first-instance","uuid":"` + firstUUID + `","approval_code":"template","open_id":"ou_submitter","status":"PENDING"}}`), nil
		}
		return nil, errors.New("unexpected native operation")
	})
	for n := range rows.rows {
		rows.rows[n].GroupValues = map[string]string{"project": rows.rows[n].RecordID}
	}
	evidence := &nativeBatchEvidence{}
	for _, id := range []string{"a", "b"} {
		request, err := appreview.PreparePinned(ctx, evidence, review.Request{LogicalID: evidence.ReviewLogicalID(id), Provider: "seal", ProviderVersion: "pin", Document: aggregate.Document{RecordID: id}})
		if err != nil {
			t.Fatal(err)
		}
		evidence.attempts = append(evidence.attempts, review.Attempt{Request: request, State: "completed", Submission: &review.Submission{DocumentID: request.Document.DocumentID, Status: "completed", Outcome: &review.Outcome{Decision: "review"}}})
	}
	gate, _ := app.NewReviewGate(evidence, evidence, "source", "seal", []string{"review"})
	source, _ := app.NewPreparedSource(&nativeBatchFields{rows}, gate)
	target, _ := gateway.Describe(ctx)
	options := core.Options{SourceScope: "source", TargetScope: target.Scope, Template: target.Template, ConfigurationVersion: target.ConfigurationVersion,
		Submitter: core.Identity{Scope: target.Scope, ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}}
	preparer, _ := app.NewRequestPreparer(source, gateway, store, options)
	prepared, err := preparer.Prepare(ctx, []string{"a", "b"})
	if err != nil || prepared.Batch == nil {
		t.Fatalf("native batch preparation failed: %v / %s", err, prepared.Issue)
	}
	batch = *prepared.Batch
	service, _ := app.NewBatchService(source, gateway, store, options)
	result, err := service.Submit(ctx, batch.ID)
	if err == nil || posts != 1 || result.Status.Plans[0].Phase != "unknown" || result.Status.Plans[1].Phase != "reserved" {
		t.Fatal("lost first native response did not stop the later POST")
	}
	// An invalid current template cannot prevent recovery of the original UUID.
	originalForm := *definition.Form
	invalid := "invalid-current-template"
	definition.Form = &invalid
	lookup, _ := NewInstanceLookupGateway(gateway.client)
	result, err = app.ReconcileBatch(ctx, store, lookup, batch.ID)
	if err != nil || gets != 1 || posts != 1 || result.Status.Plans[0].InstanceID != "first-instance" || result.Status.Plans[1].Phase != "reserved" {
		t.Fatal("query recovery used current template or lost original audit mapping")
	}
	definition.Form = &originalForm
	result, err = service.Submit(ctx, batch.ID)
	if err != nil || posts != 2 || !result.Status.AllInstancesKnown || result.Status.Plans[1].InstanceID != "second-instance" {
		t.Fatal("native batch did not resume only its unsent exact request")
	}
}
