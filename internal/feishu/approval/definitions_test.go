package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
)

type definitionTransport func(*http.Request) (*http.Response, error)

func (f definitionTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func definitionResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func exampleDefinition(t *testing.T) *larkapproval.ApprovalCreate {
	t.Helper()
	body, err := os.ReadFile("../../../configs/feishu/approval-template.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition larkapproval.ApprovalCreate
	if err := json.Unmarshal(body, &definition); err != nil {
		t.Fatal(err)
	}
	return &definition
}

func TestDefinitionDefaultClientAndOfficialContract(t *testing.T) {
	// Exercise the default HTTP client without sending anything outside the test.
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	var calls []string
	http.DefaultTransport = definitionTransport(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		if req.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			var auth map[string]string
			if err := json.NewDecoder(req.Body).Decode(&auth); err != nil {
				t.Fatal(err)
			}
			if auth["app_id"] != t.Name() || auth["app_secret"] != "test-secret" {
				t.Fatal("authentication did not use the explicit app credentials")
			}
			return definitionResponse(`{"code":0,"tenant_access_token":"test-tenant-token","expire":7200}`), nil
		}
		if req.Header.Get("Authorization") != "Bearer test-tenant-token" || req.URL.Query().Get("user_id_type") != "open_id" {
			t.Fatal("incorrect app identity or user ID type")
		}
		switch req.Method + " " + req.URL.Path {
		case "POST /open-apis/approval/v4/approvals":
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if _, ok := payload["approval_code"]; ok {
				t.Fatal("creation request could overwrite a template")
			}
			var form struct {
				Content string `json:"form_content"`
			}
			if err := json.Unmarshal(payload["form"], &form); err != nil {
				t.Fatal(err)
			}
			var widgets []struct {
				Type  string `json:"type"`
				Value []struct {
					Type string `json:"type"`
				} `json:"value"`
			}
			if err := json.Unmarshal([]byte(form.Content), &widgets); err != nil {
				t.Fatal(err)
			}
			if len(widgets) != 1 || widgets[0].Type != "fieldList" || len(widgets[0].Value) != 14 || widgets[0].Value[11].Type != "attachmentV2" {
				t.Fatal("request lost the repeatable detail or real attachment control")
			}
			return definitionResponse(`{"code":0,"data":{"approval_code":"new-code","approval_id":"123"}}`), nil
		case "GET /open-apis/approval/v4/approvals/new-code":
			return definitionResponse(`{"code":0,"data":{"approval_name":"test","status":"ACTIVE","form":"[]"}}`), nil
		default:
			return nil, fmt.Errorf("unexpected request: %s", req.URL)
		}
	})
	client, err := NewDefinitionClient(t.Name(), "test-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatal("constructing a client made a request")
	}
	created, err := client.CreateDefinition(context.Background(), exampleDefinition(t))
	if err != nil || created.Code != "new-code" || created.ID != "123" {
		t.Fatalf("creation result: %+v, %v", created, err)
	}
	definition, err := client.GetDefinition(context.Background(), created.Code)
	if err != nil || definition.Status == nil || *definition.Status != "ACTIVE" {
		t.Fatalf("readback failed: %+v, %v", definition, err)
	}
	if len(calls) != 3 {
		t.Fatalf("wanted one authentication, one creation and one read; got %v", calls)
	}
}

func TestDefinitionRejectsOverwriteBeforeNetwork(t *testing.T) {
	client, err := NewDefinitionClient(t.Name(), "test-secret", &http.Client{Transport: definitionTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid creation must not make a network request")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	definition := exampleDefinition(t)
	code := "existing-template"
	definition.ApprovalCode = &code
	if _, err := client.CreateDefinition(context.Background(), definition); err == nil || !strings.Contains(err.Error(), "approval_code") {
		t.Fatalf("expected overwrite rejection, got %v", err)
	}
	definition.ApprovalCode = nil
	form := `{}`
	definition.Form.FormContent = &form
	if _, err := client.CreateDefinition(context.Background(), definition); err == nil {
		t.Fatal("invalid form was accepted")
	}
}

func TestDefinitionAPIFailuresDoNotReportSuccessOrRetryCreation(t *testing.T) {
	for _, response := range []string{
		`{"code":99991672,"msg":"missing approval:definition"}`,
		`{"code":0,"data":{}}`,
	} {
		t.Run(response, func(t *testing.T) {
			creates := 0
			client, err := NewDefinitionClient(t.Name(), "test-secret", &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "/auth/") {
					return definitionResponse(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
				}
				creates++
				return definitionResponse(response), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.CreateDefinition(context.Background(), exampleDefinition(t)); err == nil {
				t.Fatal("unsuccessful response reported success")
			}
			if creates != 1 {
				t.Fatalf("creation called %d times", creates)
			}
		})
	}
}
