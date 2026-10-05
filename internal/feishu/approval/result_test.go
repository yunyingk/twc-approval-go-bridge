package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func TestSDKLookupPersistsTaskCommentTimelineAndModifiedRelationsWithoutURLs(t *testing.T) {
	ctx := context.Background()
	var plan core.Plan
	gets := 0
	gateway, source, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || req.URL.Path != "/open-apis/approval/v4/instances/"+plan.ID {
			t.Fatal("result lookup used another operation or identity")
		}
		gets++
		return definitionResponse(fmt.Sprintf(`{"code":0,"data":{
		"instance_code":"native","uuid":%q,"approval_code":"template","open_id":"ou_submitter","status":"REJECTED","start_time":"1000","end_time":"4000",
		"modified_instance_code":"prior-instance","reverted_instance_code":"reverted-parent",
		"task_list":[{"id":"final-task","user_id":"SHOULD_NOT_USE_TENANT_ID","open_id":"ou_actual_actor","status":"REJECTED","type":"AND","node_id":"node","node_name":"财务","custom_node_id":"finance","start_time":"2000","end_time":"4000"}],
		"comment_list":[{"id":"comment","open_id":"ou_unrelated","comment":"PRIVATE_UNRELATED_COMMENT","create_time":"4100","files":[{"url":"https://example.com/PRIVATE_SIGNED_URL","title":"附件","type":"attachment","file_size":9007199254740993}]}],
		"timeline":[{"type":"START","open_id":"ou_submitter","create_time":"1000"},{"type":"REJECT","open_id":"ou_actual_actor","task_id":"final-task","node_key":"node","comment":"PRIVATE_ACTUAL_REJECTION","create_time":"4000","ext":"{\"open_id_list\":[\"ou_cc\"],\"precision\":9007199254740993}","cc_user_list":[{"open_id":"ou_cc"}],"files":[{"url":"https://example.com/PRIVATE_ANOTHER_URL","title":"依据","type":"image"}]}]}}`, plan.ID)), nil
	})
	plan = gatewayPlan(t, gateway, source)
	store, _ := state.NewFiles(t.TempDir())
	store.BeginApproval(ctx, plan)
	lookup, _ := NewInstanceLookupGateway(gateway.client)
	reconciler, _ := app.NewReconciler(lookup, store, plan.SourceScope)
	a, err := reconciler.Reconcile(ctx, plan.ID)
	if err != nil || a.CurrentResult() == nil || gets != 1 {
		t.Fatalf("SDK result did not reach durable core: %v", err)
	}
	result := a.CurrentResult()
	decision, issue := result.HumanDecision(plan)
	if issue != "" || decision.Actor.ID != "ou_actual_actor" || decision.Actor.Scope != plan.TargetScope || decision.RejectComment != "PRIVATE_ACTUAL_REJECTION" || result.ModifiedInstanceID != "prior-instance" || result.RevertedInstanceID != "reverted-parent" || len(result.Actions) != 2 || len(result.Actions[1].Recipients) != 1 {
		t.Fatal("workflow evidence, original app identity or final reason lost")
	}
	if result.Comments[0].Files[0].Size == nil || *result.Comments[0].Files[0].Size != 9007199254740993 || result.Actions[1].Files[0].Size != nil {
		t.Fatal("attachment precision or missing size was guessed")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "SIGNED_URL") || strings.Contains(string(raw), "ANOTHER_URL") || strings.Contains(string(raw), "SHOULD_NOT_USE_TENANT_ID") {
		t.Fatal("temporary URL or unscoped tenant ID entered shared result")
	}
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("native extension metadata lost precision")
	}
	if _, err := reconciler.Reconcile(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	a, _ = store.ReadApproval(ctx, plan.SourceScope, plan.ID)
	if len(a.Results) != 1 || gets != 2 {
		t.Fatal("identical real SDK polling duplicated evidence")
	}
}

func TestSDKResultAutomaticMissingAndUnknownEvidenceNeverInventsActor(t *testing.T) {
	for _, scenario := range []struct{ kind, taskType, openID, time, want string }{
		{"automatic", "AUTO_PASS", "", "4000", "automatic_decision"},
		{"missing_actor", "AND", "", "4000", "human_actor_missing"},
		{"unknown_kind", "FUTURE_PROCESS", "ou_actor", "4000", "human_actor_missing"},
		{"unknown_time", "AND", "ou_actor", "0", "task_time_incomplete"},
	} {
		t.Run(scenario.kind, func(t *testing.T) {
			var p core.Plan
			gateway, source, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
				return definitionResponse(fmt.Sprintf(`{"code":0,"data":{"instance_code":"native","uuid":%q,"approval_code":"template","open_id":"ou_submitter","status":"APPROVED","end_time":"4000","task_list":[{"id":"task","user_id":"tenant-only","open_id":%q,"status":"APPROVED","type":%q,"end_time":%q}]}}`, p.ID, scenario.openID, scenario.taskType, scenario.time)), nil
			})
			p = gatewayPlan(t, gateway, source)
			result, err := gateway.LookupResult(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			if _, issue := result.HumanDecision(p); issue != scenario.want {
				t.Fatalf("issue=%s expected=%s", issue, scenario.want)
			}
			if scenario.openID == "" && result.Tasks[0].Actor != nil {
				t.Fatal("tenant user ID substituted for app open ID")
			}
			if scenario.kind == "unknown_kind" && result.Tasks[0].SourceKind != "FUTURE_PROCESS" {
				t.Fatal("unrecognized provider label was discarded")
			}
		})
	}
	for _, badTime := range []string{"-1", "1.5", "900719925474099312345", "not-a-time"} {
		t.Run(badTime, func(t *testing.T) {
			var p core.Plan
			gateway, source, _ := gatewayFixture(t, func(*http.Request) (*http.Response, error) {
				return definitionResponse(fmt.Sprintf(`{"code":0,"data":{"instance_code":"native","uuid":%q,"approval_code":"template","open_id":"ou_submitter","status":"APPROVED","end_time":%q}}`, p.ID, badTime)), nil
			})
			p = gatewayPlan(t, gateway, source)
			if _, err := gateway.LookupResult(context.Background(), p); err == nil {
				t.Fatal("invalid official millisecond timestamp accepted")
			}
		})
	}
}
