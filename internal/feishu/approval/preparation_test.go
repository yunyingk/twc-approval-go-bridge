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
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func TestAuditedNativeRequestMatchesExactSDKBodyAndRetainsUUIDOnLostResponse(t *testing.T) {
	posts := 0
	var expected core.RequestArtifact
	var plan core.Plan
	store, _ := state.NewFiles(t.TempDir())
	gateway, source, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != "POST" {
			t.Fatal("unexpected instance operation")
		}
		posts++
		raw, err := io.ReadAll(req.Body)
		if err != nil || string(raw) != string(expected.Body) {
			t.Fatal("actual SDK request differed from saved audit")
		}
		attempt, err := store.ReadApproval(context.Background(), plan.SourceScope, plan.ID)
		if err != nil || attempt.Phase != "submitting" {
			t.Fatal("audited request sent before member reservation")
		}
		return nil, errors.New("PRIVATE_LOST_RESPONSE")
	})
	plan = gatewayPlan(t, gateway, source)
	var err error
	expected, err = gateway.PrepareRequest(context.Background(), plan)
	if err != nil || posts != 0 || expected.Validate() != nil {
		t.Fatal("request preparation created an instance or lacked exact artifact")
	}
	var body struct {
		Form          string `json:"form"`
		AllowResubmit *bool  `json:"allow_resubmit"`
		AllowAgain    *bool  `json:"allow_submit_again"`
		UUID          string `json:"uuid"`
	}
	if json.Unmarshal(expected.Body, &body) != nil || body.UUID != plan.ID || body.AllowResubmit == nil || *body.AllowResubmit || body.AllowAgain == nil || *body.AllowAgain || !strings.Contains(body.Form, "9007199254740993.12") {
		t.Fatal("audit omitted protocol flags, stable UUID or exact form values")
	}
	audited, err := app.NewAuditedGateway(gateway, core.PreparedPlan{Plan: plan, Request: expected})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.New(source, audited, store, app.Options{SourceScope: plan.SourceScope, Submitter: plan.Submitter, Axes: plan.Axes, AllowedDecisions: plan.AllowedDecisions})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := svc.Submit(context.Background(), plan)
	if err == nil || attempt.Phase != "unknown" || posts != 1 {
		t.Fatal("lost audited creation did not retain uncertainty")
	}
	if _, err := svc.Submit(context.Background(), plan); !errors.Is(err, core.ErrReconcile) || posts != 1 {
		t.Fatal("audited rerun created another instance")
	}
}
func TestNativePreparedRequestRejectsAlteredBodyFormatAndCurrentTemplate(t *testing.T) {
	for _, scenario := range []string{"body", "format", "template"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			gateway, source, definition := gatewayFixture(t, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("forbidden") })
			plan := gatewayPlan(t, gateway, source)
			artifact, err := gateway.PrepareRequest(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "body":
				artifact, _ = core.NewRequestArtifact(artifact.Format, []byte(`{"edited":true}`))
			case "format":
				artifact.Format = "different-adapter"
			case "template":
				form := *definition.Form + " "
				definition.Form = &form
			}
			if _, err := gateway.CreatePrepared(context.Background(), plan, artifact); !errors.Is(err, core.ErrRejected) || !errors.Is(err, core.ErrPlanChanged) || calls != 0 {
				t.Fatal("altered audit or template reached instance creation")
			}
		})
	}
}
