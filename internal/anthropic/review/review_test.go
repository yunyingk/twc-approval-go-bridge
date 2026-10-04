//go:build !no_anthropic

package review

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

func TestLocalRulesAndAdvisoryDecision(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","model":"model","content":[{"type":"text","text":"{\"decision\":\"approve\",\"comment\":\"ok\",\"url\":\"https://invented.example\"}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	client, err := New("key", server.URL, "model", Rules{Version: "1", Instructions: "Check the supplied facts", RequiredContext: []string{"merchant"}})
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{}
	r.Document.DocumentID = "id"
	result, err := client.Review(context.Background(), r)
	if err != nil || result.Outcome.Decision != "review" || calls != 0 {
		t.Fatal("missing facts invoked model")
	}
	r.Context = map[string]string{"merchant": "shop"}
	result, err = client.Review(context.Background(), r)
	if err != nil || calls != 1 || result.Outcome.Decision != "review" || result.Outcome.URL != "" {
		t.Fatalf("uncontrolled decision: %#v %v", result, err)
	}
}
