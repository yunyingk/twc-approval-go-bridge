package approval

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func TestNativeRetryReusesExactSDKBodyAndUUIDConflictRequiresLookup(t *testing.T) {
	for _, scenario := range []string{"success", "uuid conflict"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			store, _ := state.NewFiles(t.TempDir())
			posts, gets := 0, 0
			var firstBody []byte
			var uuid string
			gateway, rows, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					gets++
					if req.URL.Path != "/open-apis/approval/v4/instances/"+uuid {
						t.Fatal("retry recovery queried another UUID")
					}
					return definitionResponse(`{"code":0,"data":{"instance_code":"first-instance","uuid":"` + uuid + `","approval_code":"template","open_id":"ou_submitter","status":"PENDING"}}`), nil
				}
				if req.Method != http.MethodPost {
					t.Fatal("unexpected native request")
				}
				posts++
				raw, _ := io.ReadAll(req.Body)
				var body struct {
					UUID string `json:"uuid"`
				}
				if json.Unmarshal(raw, &body) != nil {
					t.Fatal("invalid native body")
				}
				attempt, err := store.ReadApproval(ctx, "source", body.UUID)
				if err != nil || attempt.Phase != "submitting" || attempt.Audit == nil {
					t.Fatal("POST preceded durable retry run")
				}
				if posts == 1 {
					firstBody, uuid = raw, body.UUID
					resp := definitionResponse(`{"code":99991672,"msg":"PRIVATE_PERMISSION_MESSAGE"}`)
					resp.StatusCode = 400
					return resp, nil
				}
				if posts == 2 {
					if string(raw) != string(firstBody) || attempt.Run != 1 || len(attempt.ClosedRuns) != 1 || attempt.ClosedRuns[0].Proof.Failure.RemoteCode != "99991672" {
						t.Fatal("retry changed actual SDK body/UUID or dropped remote rejection")
					}
					if scenario == "uuid conflict" {
						resp := definitionResponse(`{"code":60012,"msg":"PRIVATE_UUID_CONFLICT"}`)
						resp.StatusCode = 400
						return resp, nil
					}
					return definitionResponse(`{"code":0,"data":{"instance_code":"first-instance"}}`), nil
				}
				return definitionResponse(`{"code":0,"data":{"instance_code":"second-instance"}}`), nil
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
				t.Fatal("native batch preparation failed")
			}
			batch := *prepared.Batch
			service, _ := app.NewBatchService(source, gateway, store, options)
			result, err := service.Submit(ctx, batch.ID)
			if !errors.Is(err, core.ErrRejected) || posts != 1 || !result.Status.Plans[0].Retryable || result.Status.Plans[0].NoCreation.Failure.RemoteCode != "99991672" {
				t.Fatal("native permission error not persisted as explicit rejection")
			}
			result, err = service.Retry(ctx, batch.ID)
			if scenario == "uuid conflict" {
				if err == nil || posts != 2 || result.Status.Plans[0].Phase != "unknown" || result.Status.Plans[0].NoCreation != nil || result.Status.Plans[0].Retryable || result.Status.Plans[1].Phase != "reserved" {
					t.Fatal("60012 was mistaken for another retryable rejection")
				}
				if _, err := service.Retry(ctx, batch.ID); !errors.Is(err, core.ErrReconcile) || posts != 2 {
					t.Fatal("60012 caused a third POST before lookup")
				}
				lookup, _ := NewInstanceLookupGateway(gateway.client)
				if _, err := app.ReconcileBatch(ctx, store, lookup, batch.ID); err != nil || gets != 1 || posts != 2 {
					t.Fatal("original UUID was not recovered")
				}
				result, err = service.Submit(ctx, batch.ID)
			}
			if err != nil || posts != 3 || !result.Status.AllInstancesKnown || result.Status.Plans[0].InstanceID != "first-instance" {
				t.Fatal("native retry/recovery did not resume remaining exact group")
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal("native retry diagnostic exposed upstream message")
			}
		})
	}
}
