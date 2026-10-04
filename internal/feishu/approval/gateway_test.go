package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type gatewaySource struct{ rows []core.Row }

func (*gatewaySource) ApprovalSourceScope() string { return "source" }
func (s *gatewaySource) ReadApprovalRows(_ context.Context, ids []string) ([]core.Row, error) {
	rows := []core.Row{}
	for _, id := range ids {
		for _, row := range s.rows {
			if row.RecordID == id {
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

func gatewayFixture(t *testing.T, onInstance func(*http.Request) (*http.Response, error)) (*InstanceGateway, *gatewaySource, *larkapproval.GetApprovalRespData) {
	t.Helper()
	definition, binding, values := formFixture(t)
	client, err := NewInstanceClient(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "tenant_access_token") {
			return definitionResponse(`{"code":0,"tenant_access_token":"test","expire":7200}`), nil
		}
		if req.Method == "GET" && req.URL.Path == "/open-apis/approval/v4/approvals/template" {
			body, err := json.Marshal(struct {
				Code int                               `json:"code"`
				Data *larkapproval.GetApprovalRespData `json:"data"`
			}{0, definition})
			if err != nil {
				t.Fatal(err)
			}
			return definitionResponse(string(body)), nil
		}
		return onInstance(req)
	})})
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string][]string{}
	for _, node := range definition.NodeList {
		if *node.NeedApprover {
			nodes[*node.NodeId] = []string{"ou_reviewer"}
		}
	}
	gateway, err := NewInstanceGateway(client, "template", binding, nodes)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]core.Value{}
	controls, _ := templateControls(definition)
	for _, control := range controls[0].Children {
		value := values[control.CustomID]
		kind := map[string]string{"input": "text", "textarea": "text", "date": "date", "amount": "money", "number": "number", "contact": "people", "attachmentV2": "files"}[control.Type]
		field := core.Value{Kind: kind, Text: value.Text, Decimal: value.Decimal, Currency: value.Currency}
		for _, id := range append(value.OpenIDs, value.FileCodes...) {
			field.References = append(field.References, core.Identity{Scope: gateway.TargetScope(), ID: id})
		}
		fields[control.CustomID] = field
	}
	source := &gatewaySource{}
	revision := strings.Repeat("a", 64)
	for _, id := range []string{"a", "b"} {
		source.rows = append(source.rows, core.Row{SourceScope: "source", RecordID: id, GroupValues: map[string]string{"project": "project-id"},
			Review: core.ReviewRef{DocumentID: "source:" + id + ":v:" + revision, Revision: revision, CurrentRevision: revision, State: "completed", Decision: "review"}, Fields: fields})
	}
	return gateway, source, definition
}

func gatewayPlan(t *testing.T, g *InstanceGateway, source *gatewaySource) core.Plan {
	t.Helper()
	target, err := g.Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plans, err := core.BuildPlans(core.Options{SourceScope: "source", TargetScope: target.Scope, Template: target.Template, ConfigurationVersion: target.ConfigurationVersion,
		Submitter: core.Identity{Scope: target.Scope, ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}}, source.rows)
	if err != nil || len(plans) != 1 {
		t.Fatalf("build plan: %v", err)
	}
	return plans[0]
}

func TestNativeGatewayLostCreationResponseReconcilesSavedUUIDWithNewTemplateSelected(t *testing.T) {
	posts, gets := 0, 0
	var savedUUID string
	gateway, source, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "POST":
			posts++
			var body struct {
				UUID          string `json:"uuid"`
				Form          string `json:"form"`
				OpenID        string `json:"open_id"`
				NodeApprovers []struct {
					Key   string   `json:"key"`
					Value []string `json:"value"`
				} `json:"node_approver_open_id_list"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.OpenID != "ou_submitter" || len(body.NodeApprovers) != 1 || body.NodeApprovers[0].Value[0] != "ou_reviewer" {
				t.Fatal("native creation lost scoped submitter or chosen node approver")
			}
			if strings.Count(body.Form, "9007199254740993.12") != 4 || !strings.Contains(body.Form, "widget") || strings.Count(body.Form, "approval-file-code") != 2 {
				t.Fatal("native mapping lost a row, exact money or uploaded file reference")
			}
			savedUUID = body.UUID
			return nil, errors.New("PRIVATE_LOST_RESPONSE")
		case "GET":
			gets++
			if req.URL.Path != "/open-apis/approval/v4/instances/"+savedUUID || req.URL.Query().Get("user_id_type") != "open_id" {
				t.Fatal("reconciliation queried a different identity")
			}
			return definitionResponse(fmt.Sprintf(`{"code":0,"data":{"instance_code":"instance","uuid":%q,"approval_code":"template","open_id":"ou_submitter","status":"APPROVED"}}`, savedUUID)), nil
		}
		t.Fatal("unexpected instance operation")
		return nil, errors.New("unexpected")
	})
	store, _ := state.NewFiles(t.TempDir())
	svc, err := app.New(source, gateway, store, app.Options{SourceScope: "source", Submitter: core.Identity{Scope: gateway.TargetScope(), ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := svc.Prepare(context.Background(), []string{"b", "a"})
	if err != nil || len(plans) != 1 {
		t.Fatalf("prepare live template: %v", err)
	}
	a, err := svc.Submit(context.Background(), plans[0])
	if err == nil || a.Phase != "unknown" || posts != 1 || savedUUID != plans[0].ID {
		t.Fatal("unknown response lost the original UUID")
	}
	encoded, _ := json.Marshal(a)
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("transport response body entered persistent state")
	}
	other, _ := NewInstanceGateway(gateway.client, "another-template", gateway.binding, nil)
	reconciler, _ := app.NewReconciler(other, store, "source")
	a, err = reconciler.Reconcile(context.Background(), plans[0].ID)
	if err != nil || a.Instance.Status != "approved" || !a.Instance.Verified || a.Plan.Template != "template" || gets != 1 || posts != 1 {
		t.Fatalf("native UUID recovery depended on the new template: %v", err)
	}
	lookupOnly, err := NewInstanceLookupGateway(gateway.client)
	if err != nil {
		t.Fatal(err)
	}
	withoutCurrentConfiguration, _ := app.NewReconciler(lookupOnly, store, "source")
	if _, err := withoutCurrentConfiguration.Reconcile(context.Background(), plans[0].ID); err != nil || gets != 2 || posts != 1 {
		t.Fatalf("query-only gateway required current creation configuration: %v", err)
	}
}

func TestNativeGatewayClassifiesOnlyDocumentedDefinitiveCreationErrors(t *testing.T) {
	for _, scenario := range []struct {
		status, code int
		rejected     bool
	}{
		{400, 1390001, true}, {400, 1390013, true}, {400, 1390015, true}, {400, 99991672, true},
		{400, 60012, false}, {400, 1395001, false}, {400, 1390009, false}, {503, 1390001, false},
	} {
		t.Run(fmt.Sprintf("%d-%d", scenario.status, scenario.code), func(t *testing.T) {
			posts := 0
			g, source, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" {
					t.Fatal("unexpected creation recovery request")
				}
				posts++
				resp := definitionResponse(fmt.Sprintf(`{"code":%d,"msg":"PRIVATE_FORM_BODY"}`, scenario.code))
				resp.StatusCode = scenario.status
				return resp, nil
			})
			_, err := g.Create(context.Background(), gatewayPlan(t, g, source))
			if err == nil || errors.Is(err, core.ErrRejected) != scenario.rejected || strings.Contains(err.Error(), "PRIVATE") || posts != 1 {
				t.Fatalf("unsafe creation classification or retry: %v", err)
			}
			if errors.Is(err, ErrUUIDConflict) != (scenario.code == 60012) {
				t.Fatal("UUID conflict lost its reconciliation signal")
			}
		})
	}
}

func TestNativeGatewayRejectsTemplateChangesAndIncompatibleBusinessKindsBeforePost(t *testing.T) {
	for _, change := range []string{"template", "binding", "text_as_date"} {
		t.Run(change, func(t *testing.T) {
			g, source, definition := gatewayFixture(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("changed or incompatible plan reached native creation")
				return nil, errors.New("unexpected")
			})
			plan := gatewayPlan(t, g, source)
			switch change {
			case "template":
				form := strings.Replace(*definition.Form, "widget17907391974", "new-date-widget", 1)
				definition.Form = &form
			case "binding":
				binding := g.binding
				binding.Fields["transaction_time"] = ControlSelector{ID: "new-date-widget"}
				g, _ = NewInstanceGateway(g.client, "template", binding, g.nodeApprovers)
			case "text_as_date":
				value := source.rows[0].Fields["transaction_time"]
				value.Kind = "text"
				source.rows[0].Fields["transaction_time"] = value
				plan = gatewayPlan(t, g, source)
			}
			_, err := g.Create(context.Background(), plan)
			if !errors.Is(err, core.ErrRejected) || (change != "text_as_date" && !errors.Is(err, core.ErrPlanChanged)) {
				t.Fatalf("invalid local creation was not stopped: %v", err)
			}
		})
	}
}

func TestNativeGatewayRequiresCurrentProcessApproverSelection(t *testing.T) {
	for _, scenario := range []string{"missing", "foreign_node", "duplicate", "too_many", "name"} {
		t.Run(scenario, func(t *testing.T) {
			g, _, definition := gatewayFixture(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid approver selection reached instance API")
				return nil, errors.New("unexpected")
			})
			node := *definition.NodeList[0].NodeId
			selection := map[string][]string{node: {"ou_reviewer"}}
			switch scenario {
			case "missing":
				selection = nil
			case "foreign_node":
				selection["another-node"] = []string{"ou_reviewer"}
			case "duplicate":
				selection[node] = []string{"ou_reviewer", "ou_reviewer"}
			case "too_many":
				selection[node] = []string{"ou_reviewer", "ou_second"}
			case "name":
				selection[node] = []string{"Same Display Name"}
			}
			invalid, _ := NewInstanceGateway(g.client, "template", g.binding, selection)
			if _, err := invalid.Describe(context.Background()); err == nil {
				t.Fatal("unresolved current process approvers passed readiness")
			}
		})
	}
}

func TestNativeGatewayLookupChecksSavedUUIDTemplateAndSubmitter(t *testing.T) {
	for _, field := range []string{"uuid", "approval_code", "open_id", "status"} {
		t.Run(field, func(t *testing.T) {
			var plan core.Plan
			g, source, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || !strings.HasSuffix(req.URL.Path, plan.ID) {
					t.Fatal("incorrect reconciliation target")
				}
				data := map[string]string{"instance_code": "instance", "uuid": plan.ID, "approval_code": "template", "open_id": "ou_submitter", "status": "PENDING"}
				data[field] = "foreign-or-unknown"
				body, _ := json.Marshal(map[string]any{"code": 0, "data": data})
				return definitionResponse(string(body)), nil
			})
			plan = gatewayPlan(t, g, source)
			if _, err := g.Lookup(context.Background(), plan); !errors.Is(err, core.ErrConflict) {
				t.Fatalf("foreign native observation accepted: %v", err)
			}
		})
	}
}

func TestNativeReconciliationRefusesAnotherApplicationBeforeAuthentication(t *testing.T) {
	g, source, _ := gatewayFixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("preparation unexpectedly read an instance")
		return nil, errors.New("unexpected")
	})
	plan := gatewayPlan(t, g, source)
	store, err := state.NewFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginApproval(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	other, _ := NewInstanceClient(t.Name()+"-other", "secret", &http.Client{Transport: definitionTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("saved approval UUID was sent to another application")
		return nil, errors.New("unexpected")
	})})
	lookup, _ := NewInstanceLookupGateway(other)
	reconciler, _ := app.NewReconciler(lookup, store, "source")
	if _, err := reconciler.Reconcile(context.Background(), plan.ID); err == nil {
		t.Fatal("reconciliation switched target application")
	}
	saved, err := store.ReadApproval(context.Background(), "source", plan.ID)
	if err != nil || saved.Phase != "submitting" || saved.Instance != nil {
		t.Fatal("foreign lookup changed the saved mapping")
	}
}

func TestNativeGatewayFreezesCallerMappingsAndNormalizesApproverOrder(t *testing.T) {
	g, source, definition := gatewayFixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("configuration comparison unexpectedly called an instance API")
		return nil, errors.New("unexpected")
	})
	multi := true
	definition.NodeList[0].ApproverChosenMulti = &multi
	node := *definition.NodeList[0].NodeId
	selection := map[string][]string{node: {"ou_second", "ou_reviewer"}}
	binding := DetailFormBinding{Detail: g.binding.Detail, Fields: map[string]ControlSelector{}}
	for key, selector := range g.binding.Fields {
		binding.Fields[key] = selector
	}
	first, _ := NewInstanceGateway(g.client, "template", binding, selection)
	plan := gatewayPlan(t, first, source)
	selection[node][0] = "ou_changed"
	binding.Fields["transaction_time"] = ControlSelector{ID: "changed-control"}
	if again := gatewayPlan(t, first, source); again.ID != plan.ID {
		t.Fatal("caller mapping edits changed a frozen gateway")
	}
	reordered, _ := NewInstanceGateway(g.client, "template", g.binding, map[string][]string{node: {"ou_reviewer", "ou_second"}})
	if again := gatewayPlan(t, reordered, source); again.ID != plan.ID {
		t.Fatal("approver selection order changed content identity")
	}
}

func TestNativeGatewayDoesNotExposeLegacyDefinitionErrorBodies(t *testing.T) {
	_, binding, _ := formFixture(t)
	client, _ := NewInstanceClient(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "tenant_access_token") {
			return definitionResponse(`{"code":0,"tenant_access_token":"test","expire":7200}`), nil
		}
		if req.Method != "GET" {
			t.Fatal("definition failure reached creation")
		}
		resp := definitionResponse(`{"code":1390002,"msg":"PRIVATE_TEMPLATE_INFORMATION"}`)
		resp.StatusCode = 400
		return resp, nil
	})})
	gateway, _ := NewInstanceGateway(client, "template", binding, nil)
	if _, err := gateway.Describe(context.Background()); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("legacy definition response body reached common approval errors")
	}
}
