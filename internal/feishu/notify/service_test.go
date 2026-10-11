package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildDeadLetterCard(t *testing.T) {
	notice := DeadLetterNotice{
		RecordID:  "rec_test123",
		RecordURL: "https://zyt-test.feishu.cn/base/app123?table=tbl123&record=rec_test123",
		FileName:  "receipt_sample.jpg",
		FileToken: "boxcn_sample_token",
		Attempts:  3,
		LastError: "400 Bad Request: unsupported image format",
		FailedAt:  time.Date(2026, 10, 11, 20, 0, 0, 0, time.Local),
	}

	cardMap := BuildDeadLetterCardMap(notice)
	header, ok := cardMap["header"].(map[string]any)
	if !ok {
		t.Fatalf("expected header object")
	}
	if header["template"] != "carmine" {
		t.Errorf("expected template 'carmine', got %v", header["template"])
	}

	cardJSON, err := BuildDeadLetterCardJSON(notice)
	if err != nil {
		t.Fatalf("BuildDeadLetterCardJSON failed: %v", err)
	}

	if !strings.Contains(cardJSON, "rec_test123") {
		t.Errorf("expected card JSON to contain record ID")
	}
	if !strings.Contains(cardJSON, "receipt_sample.jpg") {
		t.Errorf("expected card JSON to contain file name")
	}
	if !strings.Contains(cardJSON, "unsupported image format") {
		t.Errorf("expected card JSON to contain last error")
	}
	if !strings.Contains(cardJSON, "在飞书中查看单据") {
		t.Errorf("expected card JSON to contain button when RecordURL is present")
	}

	// Without RecordURL
	noticeNoURL := notice
	noticeNoURL.RecordURL = ""
	cardJSONNoURL, err := BuildDeadLetterCardJSON(noticeNoURL)
	if err != nil {
		t.Fatalf("BuildDeadLetterCardJSON without URL failed: %v", err)
	}
	if strings.Contains(cardJSONNoURL, "在飞书中查看单据") {
		t.Errorf("did not expect button when RecordURL is empty")
	}
}

func TestBuildTestCard(t *testing.T) {
	notice := TestNotice{
		TriggeredBy: "admin_test",
		Timestamp:   time.Now(),
		Note:        "manual connectivity verification",
	}

	cardJSON, err := BuildTestCardJSON(notice)
	if err != nil {
		t.Fatalf("BuildTestCardJSON failed: %v", err)
	}
	if !strings.Contains(cardJSON, "admin_test") {
		t.Errorf("expected card to contain TriggeredBy")
	}
	if !strings.Contains(cardJSON, "manual connectivity verification") {
		t.Errorf("expected card to contain Note")
	}
}

func TestServiceBroadcastAndDedup(t *testing.T) {
	var chatCalls int32
	var userCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock token endpoint
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"mock_token","expire":7200}`))
			return
		}

		// Mock message endpoint
		if strings.HasSuffix(r.URL.Path, "/im/v1/messages") {
			queryType := r.URL.Query().Get("receive_id_type")
			bodyBytes, _ := io.ReadAll(r.Body)
			var payload struct {
				ReceiveID string `json:"receive_id"`
				MsgType   string `json:"msg_type"`
				Content   string `json:"content"`
			}
			_ = json.Unmarshal(bodyBytes, &payload)

			if queryType == "chat_id" {
				atomic.AddInt32(&chatCalls, 1)
			} else if queryType == "open_id" {
				atomic.AddInt32(&userCalls, 1)
			}

			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":0,"msg":"ok","data":{"message_id":"om_mock_123"}}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient("cli_mock", "sec_mock", WithAPIBase(server.URL), WithHTTPClient(server.Client()))

	svc := NewService(Config{
		Enabled:            true,
		ChatIDs:            []string{"oc_group1", "oc_group2"},
		OpenIDs:            []string{"ou_user1"},
		NotifyOnDeadLetter: true,
	}, client, nil)

	notice := DeadLetterNotice{
		RecordID:  "rec_batch_001",
		FileName:  "invoice.pdf",
		FileToken: "box_token_1",
		Attempts:  3,
		LastError: "timeout",
		FailedAt:  time.Now(),
	}

	ctx := context.Background()

	// 1. First notification should dispatch to 2 chats and 1 user
	if err := svc.NotifyDeadLetter(ctx, notice); err != nil {
		t.Fatalf("first NotifyDeadLetter failed: %v", err)
	}

	if atomic.LoadInt32(&chatCalls) != 2 {
		t.Errorf("expected 2 chat calls, got %d", atomic.LoadInt32(&chatCalls))
	}
	if atomic.LoadInt32(&userCalls) != 1 {
		t.Errorf("expected 1 user call, got %d", atomic.LoadInt32(&userCalls))
	}

	// 2. Duplicate notification within 1 hour should be skipped
	if err := svc.NotifyDeadLetter(ctx, notice); err != nil {
		t.Fatalf("second NotifyDeadLetter failed: %v", err)
	}

	if atomic.LoadInt32(&chatCalls) != 2 || atomic.LoadInt32(&userCalls) != 1 {
		t.Errorf("expected no additional calls for duplicate notice, got chats=%d, users=%d",
			atomic.LoadInt32(&chatCalls), atomic.LoadInt32(&userCalls))
	}

	// 3. Test sending a test connectivity card
	results, err := svc.SendTestCard(ctx, TestNotice{TriggeredBy: "tester", Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("SendTestCard failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 delivery results, got %d", len(results))
	}
}
