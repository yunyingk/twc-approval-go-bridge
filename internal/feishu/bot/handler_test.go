package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/notify"
	"github.com/yunyingk/twc-approval-go-bridge/internal/httpserver"
)

func TestExtractPlainText(t *testing.T) {
	cases := []struct {
		msgType string
		content string
		want    string
	}{
		{"text", `{"text":"管理页面"}`, "管理页面"},
		{"text", `{"text":"@_user_1 管理页面"}`, "管理页面"},
		{"text", `{"text":"@影视飓风 查看状态"}`, "查看状态"},
		{"text", `{"text":"  看板  "}`, "看板"},
		{"image", `{"image_key":"img_123"}`, ""},
	}

	for _, c := range cases {
		got := ExtractPlainText(c.msgType, c.content)
		if got != c.want {
			t.Errorf("ExtractPlainText(%q, %q) = %q, want %q", c.msgType, c.content, got, c.want)
		}
	}
}

func TestMatchIntent(t *testing.T) {
	cases := []struct {
		input string
		want  Intent
	}{
		{"管理页面", IntentDashboard},
		{"看板", IntentDashboard},
		{"dashboard", IntentDashboard},
		{"打开后台", IntentDashboard},
		{"控制台", IntentDashboard},
		{"状态", IntentStatus},
		{"status", IntentStatus},
		{"服务健康度", IntentStatus},
		{"帮助", IntentHelp},
		{"help", IntentHelp},
		{"？", IntentHelp},
		{"今天天气怎么样", IntentUnknown},
	}

	for _, c := range cases {
		got := MatchIntent(c.input)
		if got != c.want {
			t.Errorf("MatchIntent(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func buildTestEvent(senderID, chatType, text, msgID string) events.Event {
	payload := map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"event_id":   "ev_" + msgID,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{
				"sender_id": map[string]any{
					"open_id": senderID,
				},
				"sender_type": "user",
			},
			"message": map[string]any{
				"message_id":   msgID,
				"chat_type":    chatType,
				"chat_id":      "oc_chat123",
				"message_type": "text",
				"content":      `{"text":"` + text + `"}`,
			},
		},
	}
	b, _ := json.Marshal(payload)
	return events.Event{
		ID:      "ev_" + msgID,
		Type:    "im.message.receive_v1",
		Payload: b,
	}
}

func TestHandler_AuthorizedDashboardCommand(t *testing.T) {
	var mu sync.Mutex
	var sentPayloads []map[string]any

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "mock_token", "expire": 7200,
			})
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		sentPayloads = append(sentPayloads, req)
		mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{"message_id": "om_reply123"},
		})
	}))
	defer mockServer.Close()

	client := notify.NewClient("cli_app", "secret",
		notify.WithAPIBase(mockServer.URL),
		notify.WithHTTPClient(mockServer.Client()),
	)
	tm := httpserver.NewTokenManager(2 * time.Hour)

	h := NewHandler(Config{
		PublicBaseURL: "https://twc-approval.ying-qing.cn",
		AdminOpenIDs:  []string{"ou_admin1"},
		TokenTTL:      2 * time.Hour,
	}, client, tm, nil, nil)

	// 1. Authorized user sends "管理页面"
	ev := buildTestEvent("ou_admin1", "p2p", "管理页面", "msg_001")
	err := h.Sink(context.Background(), ev)
	if err != nil {
		t.Fatalf("Sink failed: %v", err)
	}

	mu.Lock()
	count := len(sentPayloads)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected 1 reply sent, got %d", count)
	}

	// Verify active magic token in TokenManager
	active := tm.ActiveTokens()
	if len(active) != 1 {
		t.Fatalf("expected 1 active magic token, got %d", len(active))
	}
	if active[0].UserID != "ou_admin1" {
		t.Fatalf("expected token user to be ou_admin1, got %s", active[0].UserID)
	}

	// Verify card contains the token URL
	cardContent := sentPayloads[0]["content"].(string)
	if !strings.Contains(cardContent, active[0].Token) {
		t.Fatalf("expected card to contain token %s", active[0].Token)
	}
	if !strings.Contains(cardContent, "https://twc-approval.ying-qing.cn/?token=") {
		t.Fatalf("expected card to contain magic URL")
	}

	// 2. Duplicate message should be skipped
	err = h.Sink(context.Background(), ev)
	if err != nil {
		t.Fatalf("Sink duplicate failed: %v", err)
	}
	mu.Lock()
	if len(sentPayloads) != 1 {
		t.Fatalf("expected duplicate message to be suppressed")
	}
	mu.Unlock()
}

func TestHandler_UnauthorizedUser(t *testing.T) {
	var mu sync.Mutex
	var sentPayloads []map[string]any

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "mock_token", "expire": 7200,
			})
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		sentPayloads = append(sentPayloads, req)
		mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{"message_id": "om_reply123"},
		})
	}))
	defer mockServer.Close()

	client := notify.NewClient("cli_app", "secret",
		notify.WithAPIBase(mockServer.URL),
		notify.WithHTTPClient(mockServer.Client()),
	)
	tm := httpserver.NewTokenManager(2 * time.Hour)

	h := NewHandler(Config{
		PublicBaseURL: "https://twc-approval.ying-qing.cn",
		AdminOpenIDs:  []string{"ou_admin1"}, // only ou_admin1
		TokenTTL:      2 * time.Hour,
	}, client, tm, nil, nil)

	// Unauthorized user sends "管理页面"
	ev := buildTestEvent("ou_stranger", "p2p", "管理页面", "msg_stranger")
	err := h.Sink(context.Background(), ev)
	if err != nil {
		t.Fatalf("Sink failed: %v", err)
	}

	// No token should be created
	if len(tm.ActiveTokens()) != 0 {
		t.Fatalf("no tokens should be generated for unauthorized user")
	}

	mu.Lock()
	cardContent := sentPayloads[0]["content"].(string)
	mu.Unlock()
	if !strings.Contains(cardContent, "访问未授权") {
		t.Fatalf("expected unauthorized notice in card, got: %s", cardContent)
	}
}

func TestHandler_StatusCommand(t *testing.T) {
	var mu sync.Mutex
	var sentPayloads []map[string]any

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "mock_token", "expire": 7200,
			})
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		sentPayloads = append(sentPayloads, req)
		mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{"message_id": "om_reply123"},
		})
	}))
	defer mockServer.Close()

	client := notify.NewClient("cli_app", "secret",
		notify.WithAPIBase(mockServer.URL),
		notify.WithHTTPClient(mockServer.Client()),
	)
	tm := httpserver.NewTokenManager(2 * time.Hour)

	h := NewHandler(Config{
		PublicBaseURL: "https://twc-approval.ying-qing.cn",
	}, client, tm, func() httpserver.Status {
		return httpserver.Status{
			Version:         "0.3.2",
			Uptime:          "1h 23m",
			FeishuState:     "ready",
			ReceiptProvider: "anyreceipt",
			ReviewProvider:  "seal",
		}
	}, nil)

	ev := buildTestEvent("ou_user", "p2p", "状态", "msg_status")
	err := h.Sink(context.Background(), ev)
	if err != nil {
		t.Fatalf("Sink failed: %v", err)
	}

	mu.Lock()
	cardContent := sentPayloads[0]["content"].(string)
	mu.Unlock()
	if !strings.Contains(cardContent, "系统实时运行状态") || !strings.Contains(cardContent, "0.3.2") {
		t.Fatalf("expected status info in card, got: %s", cardContent)
	}
}
