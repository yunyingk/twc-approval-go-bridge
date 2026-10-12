package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type dummyTokenProvider struct{}

func (dummyTokenProvider) TenantAccessToken(ctx context.Context) (string, error) {
	return "test_tenant_token", nil
}

type mockSource struct {
	detail core.Detail
	err    error
}

func (m *mockSource) ReadDetail(ctx context.Context, recordID string) (core.Detail, error) {
	if m.err != nil {
		return core.Detail{}, m.err
	}
	return m.detail, nil
}

type mockReviewReader struct {
	outcome core.Outcome
}

func (m *mockReviewReader) ReadReview(ctx context.Context, documentID string) (core.Attempt, error) {
	return core.Attempt{
		Submission: &core.Submission{
			Outcome: &m.outcome,
		},
	}, nil
}

func TestMapperBuildCreateInstanceRequest(t *testing.T) {
	input := ApprovalInput{
		RecordID:         "rec_detail_001",
		DocumentSN:       "EXP-DEMO-001",
		Title:            "海外易商卡消费确认 - 测试商户 USD 100",
		SubmissionDate:   time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC),
		TransactionID:    "tx_999",
		CardholderOpenID: "ou_user_123",
		ExpenseDate:      "2026-10-10",
		ExpenseCategory:  "市场推广",
		ProjectCode:      "PRJ-01",
		ExpenseReason:    "日常营销测试",
		OriginalAmount:   100.0,
		CNYAmount:        720.0,
		Currency:         "USD",
		ReviewStatus:     "pass",
		ReviewComment:    "机审规则全部通过",
		RiskLevel:        "low",
		Locked:           false,
		ApproverOpenID:   "ou_approver_456",
		InitiatorOpenID:  "ou_initiator_789",
	}

	req, err := BuildCreateInstanceRequest(DefaultApprovalCode, input)
	if err != nil {
		t.Fatalf("BuildCreateInstanceRequest failed: %v", err)
	}

	if req.ApprovalCode != DefaultApprovalCode {
		t.Errorf("expected approval code %s, got %s", DefaultApprovalCode, req.ApprovalCode)
	}
	if req.OpenID != "ou_initiator_789" {
		t.Errorf("expected initiator open_id ou_initiator_789, got %s", req.OpenID)
	}
	if len(req.NodeApproverList) != 1 || req.NodeApproverList[0].Key != CustomNodeApproval || req.NodeApproverList[0].Value[0] != "ou_approver_456" {
		t.Errorf("unexpected node approver list: %+v", req.NodeApproverList)
	}

	var formControls []FormControl
	if err := json.Unmarshal([]byte(req.Form), &formControls); err != nil {
		t.Fatalf("unmarshal form failed: %v", err)
	}
	if len(formControls) != 3 {
		t.Fatalf("expected 3 top-level form controls, got %d", len(formControls))
	}
	if formControls[0].ID != WidgetTitle || formControls[0].Value != input.Title {
		t.Errorf("unexpected title control: %+v", formControls[0])
	}
	if formControls[1].ID != WidgetSubmitDate || formControls[1].Value != "2026-10-12" {
		t.Errorf("unexpected date control: %+v", formControls[1])
	}
	if formControls[2].ID != WidgetDetailList || formControls[2].Type != "fieldList" {
		t.Errorf("unexpected fieldList control: %+v", formControls[2])
	}
}

func TestClientCreateInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test_tenant_token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req CreateInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.UUID == "conflict-uuid" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":60012,"msg":"uuid already exists"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"msg":"success","data":{"instance_code":"INST_123456","instance_link":"https://feishu.cn/approval/s/INST_123456"}}`))
	}))
	defer server.Close()

	client := NewClientWithTokenProvider(dummyTokenProvider{},
		WithEndpoint(server.URL),
		WithHTTPClient(server.Client()))

	req := CreateInstanceRequest{
		ApprovalCode: DefaultApprovalCode,
		Form:         "[]",
		UUID:         "uuid-normal",
	}

	resp, err := client.CreateInstance(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateInstance failed: %v", err)
	}
	if resp.InstanceCode != "INST_123456" {
		t.Errorf("expected instance code INST_123456, got %s", resp.InstanceCode)
	}

	// Conflict test
	req.UUID = "conflict-uuid"
	_, errConflict := client.CreateInstance(context.Background(), req)
	if errConflict != ErrUUIDConflict {
		t.Errorf("expected ErrUUIDConflict, got %v", errConflict)
	}
}

func TestServicePreviewAndPush(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"msg":"success","data":{"instance_code":"INST_TEST","instance_link":"https://feishu.cn/approval/s/INST_TEST"}}`))
	}))
	defer server.Close()

	client := NewClientWithTokenProvider(dummyTokenProvider{},
		WithEndpoint(server.URL),
		WithHTTPClient(server.Client()))

	source := &mockSource{
		detail: core.Detail{
			DocumentID: "feishu:base:table:rec_1",
			DocumentSN: "EXP-1",
			RecordID:   "rec_1",
			Context: map[string]string{
				"expense_category": "市场推广",
				"expense_reason":   "单元测试",
			},
			Transactions: &core.TransactionEvidence{
				Transactions: []core.Transaction{
					{
						TransactionID:    "TX_100",
						Merchant:         "Google Cloud",
						OriginalAmount:   "50.0",
						OriginalCurrency: "USD",
						BookedAmountCNY:  "360.0",
					},
				},
			},
		},
	}
	reader := &mockReviewReader{
		outcome: core.Outcome{
			Decision: "approve",
			Comment:  "自动核验通过",
		},
	}

	service := NewService(client, ServiceOptions{
		ApprovalCode:     DefaultApprovalCode,
		DefaultApprover:  "ou_admin",
		DefaultInitiator: "ou_bot",
		Source:           source,
		ReviewReader:     reader,
	})

	preview, err := service.Preview(context.Background(), "rec_1")
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if preview.ApprovalCode != DefaultApprovalCode {
		t.Errorf("unexpected approval code in preview: %s", preview.ApprovalCode)
	}

	resp, err := service.Push(context.Background(), "rec_1")
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if resp.InstanceCode != "INST_TEST" {
		t.Errorf("expected instance code INST_TEST, got %s", resp.InstanceCode)
	}
}
