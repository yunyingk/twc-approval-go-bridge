//go:build !no_anthropic

package review

import (
	"context"
	"encoding/json"
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

func TestSelfHostedReviewUsesSharedPaymentsAndStopsOnMissingEvidence(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var input struct {
			Transactions *core.TransactionEvidence `json:"transactions"`
		}
		if err := json.Unmarshal([]byte(body.Messages[0].Content[0].Text), &input); err != nil || input.Transactions == nil || input.Transactions.Transactions[0].OriginalAmount != "9007199254740993.01" {
			t.Error("model did not receive exact shared payment evidence")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","model":"model","content":[{"type":"text","text":"{\"decision\":\"approve\",\"comment\":\"ok\"}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	client, err := New("key", server.URL, "model", Rules{Version: "1", Instructions: "Check supplied payment facts", AllowAutomaticDecision: true})
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{Transactions: &core.TransactionEvidence{Source: "payments", LinkedRecordIDs: []string{"payment"}, Transactions: []core.Transaction{{RecordID: "payment", OriginalAmount: "9007199254740993.01", OriginalCurrency: "USD"}}, Issues: []core.EvidenceIssue{{Code: "missing_relation"}}}}
	r.Document.DocumentID = "id"
	result, err := client.Review(context.Background(), r)
	if err != nil || calls != 0 || result.Outcome.Decision != "review" {
		t.Fatal("incomplete payment evidence invoked model or became an automatic decision")
	}
	r.Transactions.Issues = nil
	result, err = client.Review(context.Background(), r)
	if err != nil || calls != 1 || result.Outcome.Decision != "approve" {
		t.Fatalf("complete payment evidence failed: %v", err)
	}
}
