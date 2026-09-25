package anyreceipt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRecognizeUsesExistingAnyreceiptContract(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != summaryURL {
			t.Errorf("request = %s %s", req.Method, req.URL)
		}
		if req.Header.Get("X-API-KEY") != "test-key" {
			t.Error("API key header missing")
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["url"] != "https://example.com/receipt.pdf" || body["fileType"] != "application/pdf" {
			t.Errorf("unexpected request body: %#v", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"outputs":{"title":"Lunch","total":12.50,"DateofInssuance":"2026-09-25"}}}`)),
			Header:     make(http.Header),
		}, nil
	})}
	client, err := New("test-key", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Recognize(context.Background(), receipt.Attachment{
		Name: "receipt.pdf", ContentType: "application/pdf", URL: "https://example.com/receipt.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Lunch" || got.Total != "12.50" || got.Date != "2026-09-25" {
		t.Errorf("recognition = %#v", got)
	}
}

func TestRecognizeRejectsBusinessError(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"errorCode":401,"errorMessage":"invalid API key"}`)),
			Header:     make(http.Header),
		}, nil
	})}
	client, err := New("test-key", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Recognize(context.Background(), receipt.Attachment{URL: "https://example.com/receipt.pdf"}); err == nil {
		t.Fatal("Recognize() error = nil, want business error")
	}
}
