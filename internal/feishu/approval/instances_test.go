package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInstanceLostResponseCanBeReadByOriginalUUIDWithoutAnotherCreate(t *testing.T) {
	posts, gets, auths := 0, 0, 0
	var wireForm string
	transport := definitionTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			auths++
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["app_id"] != t.Name() || body["app_secret"] != "secret" {
				t.Fatal("instance used another application's credentials")
			}
			return definitionResponse(`{"code":0,"tenant_access_token":"tenant-test","expire":7200}`), nil
		}
		if req.Header.Get("Authorization") != "Bearer tenant-test" {
			t.Fatal("instance did not use application identity")
		}
		switch req.Method + " " + req.URL.Path {
		case "POST /open-apis/approval/v4/instances":
			posts++
			var body struct {
				ApprovalCode     string `json:"approval_code"`
				OpenID           string `json:"open_id"`
				UUID             string `json:"uuid"`
				Form             string `json:"form"`
				AllowResubmit    *bool  `json:"allow_resubmit"`
				AllowSubmitAgain *bool  `json:"allow_submit_again"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.UUID != "stable-uuid" || body.OpenID != "ou_submitter" || body.ApprovalCode != "template" {
				t.Fatal("creation changed caller's frozen identity or UUID")
			}
			if body.AllowResubmit == nil || *body.AllowResubmit || body.AllowSubmitAgain == nil || *body.AllowSubmitAgain {
				t.Fatal("creation enabled untracked resubmission")
			}
			wireForm = body.Form
			return nil, errors.New("PRIVATE_LOST_RESPONSE")
		case "GET /open-apis/approval/v4/instances/stable-uuid":
			gets++
			if req.URL.Query().Get("user_id_type") != "open_id" {
				t.Fatal("UUID lookup changed identity type")
			}
			return definitionResponse(`{"code":0,"data":{"instance_code":"instance","approval_code":"template","uuid":"stable-uuid","open_id":"ou_submitter","status":"PENDING"}}`), nil
		default:
			return nil, fmt.Errorf("unexpected instance request")
		}
	})
	client, err := NewInstanceClient(t.Name(), "secret", &http.Client{Transport: transport})
	if err != nil || auths != 0 {
		t.Fatal("construction made a network request")
	}
	form := json.RawMessage(`[{"id":"lines","type":"fieldList","value":[[{"id":"amount","type":"amount","value":9007199254740993.12,"currency":"USD"}]]}]`)
	if _, err := client.CreateInstance(context.Background(), InstanceRequest{ApprovalCode: "template", OpenID: "ou_submitter", UUID: "stable-uuid", Form: form}); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("lost response was accepted or exposed upstream contents")
	}
	if !strings.Contains(wireForm, "9007199254740993.12") {
		t.Fatal("amount lost decimal precision")
	}
	instance, err := client.GetInstance(context.Background(), "stable-uuid")
	if err != nil || *instance.InstanceCode != "instance" || *instance.Uuid != "stable-uuid" || posts != 1 || gets != 1 || auths != 1 {
		t.Fatal("UUID recovery repeated creation or lost mapping")
	}
}

func TestInstanceConflictAndSafeRemoteErrors(t *testing.T) {
	for _, code := range []int{60012, 1390001, 1395001} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			posts := 0
			client, err := NewInstanceClient(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "tenant_access_token") {
					return definitionResponse(`{"code":0,"tenant_access_token":"test","expire":7200}`), nil
				}
				posts++
				return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"code":%d,"msg":"PRIVATE_FORM_CONTENT"}`, code)))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.CreateInstance(context.Background(), InstanceRequest{ApprovalCode: "template", OpenID: "ou_submitter", UUID: "uuid", Form: json.RawMessage(`[{"id":"text","type":"input","value":"PRIVATE_VALUE"}]`)})
			var remote *InstanceAPIError
			if !errors.As(err, &remote) || remote.Code != code || remote.HTTPStatus() != 400 || strings.Contains(err.Error(), "PRIVATE") || posts != 1 {
				t.Fatal("unsafe or missing failure metadata, or automatic request retry")
			}
			if errors.Is(err, ErrUUIDConflict) != (code == 60012) {
				t.Fatal("UUID collision was treated as an ordinary rejection")
			}
		})
	}
}

func TestInstanceValidationStopsBeforeAuthentication(t *testing.T) {
	client, _ := NewInstanceClient(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid native request reached the network")
		return nil, errors.New("unexpected")
	})})
	for _, form := range []string{`[]`, `[{"id":"money","type":"amount","value":"1,23","currency":"USD"}]`, `[{"id":"person","type":"contact","value":["name"]}]`, `[{"id":"lines","type":"fieldList","value":[[{"id":"a","type":"input","value":"ok","unexpected":"value"}]]}]`, `[{"id":"base","type":"mutableGroup","value":{}}]`} {
		if _, err := client.CreateInstance(context.Background(), InstanceRequest{ApprovalCode: "template", OpenID: "ou_submitter", UUID: "uuid", Form: json.RawMessage(form)}); err == nil {
			t.Fatalf("invalid form accepted: %s", form)
		}
	}
}
