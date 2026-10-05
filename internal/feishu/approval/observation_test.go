package approval

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

func TestNativeLookupUsesGetRevertedBooleanInsteadOfEventDecision(t *testing.T) {
	for _, scenario := range []struct {
		status   string
		reverted bool
		want     string
		conflict bool
	}{
		{"APPROVED", true, "reverted", false}, {"APPROVED", false, "approved", false}, {"PENDING", true, "", true}, {"CANCELED", true, "canceled", false},
	} {
		t.Run(fmt.Sprintf("%s-%t", scenario.status, scenario.reverted), func(t *testing.T) {
			var plan core.Plan
			queries := 0
			gateway, source, _ := gatewayFixture(t, func(req *http.Request) (*http.Response, error) {
				queries++
				if req.Method != "GET" || req.URL.Path != "/open-apis/approval/v4/instances/"+plan.ID {
					t.Fatal("observer used native event ID/code or creation API")
				}
				return definitionResponse(fmt.Sprintf(`{"code":0,"data":{"instance_code":"native","uuid":%q,"approval_code":"template","open_id":"ou_submitter","status":%q,"reverted":%t}}`, plan.ID, scenario.status, scenario.reverted)), nil
			})
			plan = gatewayPlan(t, gateway, source)
			lookup, _ := NewInstanceLookupGateway(gateway.client)
			instance, err := lookup.Lookup(context.Background(), plan)
			if scenario.conflict {
				if !errors.Is(err, core.ErrConflict) {
					t.Fatal("pending/reverted contradiction was accepted")
				}
			} else if err != nil || instance.Status != scenario.want || !instance.Verified {
				t.Fatalf("unexpected native mapping: %v / %s", err, instance.Status)
			}
			if queries != 1 {
				t.Fatal("lookup retried or performed unrelated request")
			}
		})
	}
}

func TestNativeSubscriptionUsesOneSDKPostAndDoesNotTreat1390007AsActive(t *testing.T) {
	for _, scenario := range []struct {
		body    string
		status  int
		success bool
		code    int
	}{
		{`{"code":0}`, 200, true, 0}, {`{"code":1390007,"msg":"PRIVATE_SUBSCRIPTION_MESSAGE"}`, 400, false, 1390007}, {`{"data":{},"PRIVATE":"body"}`, 200, false, 0}, {`{"code":0}`, 503, false, 0},
	} {
		t.Run(fmt.Sprintf("%d-%d-%t", scenario.status, scenario.code, scenario.success), func(t *testing.T) {
			posts := 0
			client, _ := NewInstanceClient(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "tenant_access_token") {
					return definitionResponse(`{"code":0,"tenant_access_token":"test","expire":7200}`), nil
				}
				posts++
				if req.Method != "POST" || req.URL.Path != "/open-apis/approval/v4/approvals/template/subscribe" || req.Header.Get("Authorization") != "Bearer test" {
					t.Fatal("subscription used another path or identity")
				}
				resp := definitionResponse(scenario.body)
				resp.StatusCode = scenario.status
				return resp, nil
			})})
			err := client.SubscribeApprovalEvents(context.Background(), "template")
			if posts != 1 || (err == nil) != scenario.success || (err != nil && strings.Contains(err.Error(), "PRIVATE")) {
				t.Fatalf("subscription result/retry/privacy: %v, %d", err, posts)
			}
			if scenario.code != 0 {
				var api *InstanceAPIError
				if !errors.As(err, &api) || api.Code != scenario.code {
					t.Fatal("ambiguous subscription diagnosis lost")
				}
			}
		})
	}
}
