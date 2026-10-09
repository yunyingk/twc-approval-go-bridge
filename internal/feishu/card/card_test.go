package card

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildTransactionCardJSON(t *testing.T) {
	notice := TransactionNotice{
		TransactionID:   "202609120009820201",
		TransactionTime: "2026/09/12",
		Merchant:        "Z Y RESTAURANT SAN FRANCISCO USA",
		BookedAmount:    "CNY 1462.55",
		OriginalAmount:  "USD 217.27",
		FormURL:         "https://zyt-test.feishu.cn/share/base/shrcnhMrnWHtHGvgc2G1nHIMDwf",
		NoteText:        "来自 海外易商卡 × 影视飓风 报销助手",
	}

	cardJSON, err := BuildTransactionCardJSON(notice)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	header, ok := parsed["header"].(map[string]any)
	if !ok || header["template"] != "turquoise" {
		t.Fatalf("expected turquoise template, got: %v", header)
	}

	elements, ok := parsed["elements"].([]any)
	if !ok || len(elements) < 3 {
		t.Fatalf("expected elements, got: %v", elements)
	}

	// Verify text content
	div := elements[0].(map[string]any)
	textContent := div["text"].(map[string]any)["content"].(string)
	if !strings.Contains(textContent, "202609120009820201") ||
		!strings.Contains(textContent, "Z Y RESTAURANT") ||
		!strings.Contains(textContent, "CNY 1462.55") {
		t.Fatalf("content missing expected fields: %s", textContent)
	}
}

func TestSendCardToUser_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"mock-token"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/im/v1/messages") {
			if r.Header.Get("Authorization") != "Bearer mock-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["receive_id"] != "ou_test123" || body["msg_type"] != "interactive" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"message_id":"om_msg_test_001"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("app", "secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()

	notice := TransactionNotice{
		TransactionID:   "202609120009820201",
		TransactionTime: "2026/09/12",
		Merchant:        "Test Merchant",
		BookedAmount:    "CNY 100",
		OriginalAmount:  "USD 15",
		FormURL:         "https://test.form",
	}

	msgID, err := client.SendCardToUser(context.Background(), "ou_test123", notice)
	if err != nil {
		t.Fatalf("send card failed: %v", err)
	}
	if msgID != "om_msg_test_001" {
		t.Fatalf("unexpected msgID: %s", msgID)
	}
}

func TestBuildFormURL(t *testing.T) {
	// 1. With enterprise host and form share token + prefill
	url1 := BuildFormURL("https://zyt-test.feishu.cn", "shrcnhMrnWHtHGvgc2G1nHIMDwf", "关联交易流水号", "rec123456")
	expected1 := "https://zyt-test.feishu.cn/share/base/form/shrcnhMrnWHtHGvgc2G1nHIMDwf?prefill_关联交易流水号=rec123456"
	if url1 != expected1 {
		t.Fatalf("expected %s, got %s", expected1, url1)
	}

	// 2. Host without scheme
	url2 := BuildFormURL("zyt-test.feishu.cn/", "shrcnhMrnWHtHGvgc2G1nHIMDwf", "", "")
	expected2 := "https://zyt-test.feishu.cn/share/base/form/shrcnhMrnWHtHGvgc2G1nHIMDwf"
	if url2 != expected2 {
		t.Fatalf("expected %s, got %s", expected2, url2)
	}

	// 3. Direct full form URL
	url3 := BuildFormURL("", "https://zyt-test.feishu.cn/share/base/form/shrcnhMrnWHtHGvgc2G1nHIMDwf", "关联交易流水号", "rec999")
	expected3 := "https://zyt-test.feishu.cn/share/base/form/shrcnhMrnWHtHGvgc2G1nHIMDwf?prefill_关联交易流水号=rec999"
	if url3 != expected3 {
		t.Fatalf("expected %s, got %s", expected3, url3)
	}

	// 4. Empty token returns empty
	if BuildFormURL("https://zyt-test.feishu.cn", "", "", "") != "" {
		t.Fatalf("expected empty URL for empty token")
	}
}

