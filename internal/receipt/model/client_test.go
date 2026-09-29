package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
)

func TestAnthropicCompatibleImageRecognition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "deepseek-flash" || len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 || body.Messages[0].Content[1]["type"] != "image" {
			t.Errorf("unexpected model request: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"{\"outputs\":{\"total\":\"12\",\"extra\":\"kept\"},\"summary\":\"ok\"}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer server.Close()
	client, err := New("test-key", server.URL, "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Recognize(context.Background(), receipt.Attachment{ContentType: "image/png", Data: []byte("image bytes")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "ok" || string(result.Outputs["extra"]) != `"kept"` {
		t.Fatalf("result = %+v", result)
	}
	if _, err := client.Recognize(context.Background(), receipt.Attachment{ContentType: "application/pdf", Data: []byte("%PDF")}); err == nil {
		t.Fatal("PDF unexpectedly accepted")
	}
}
