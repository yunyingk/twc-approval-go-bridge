package anyreceipt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
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
	got, err := client.Recognize(context.Background(), invoice.Attachment{
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
	if _, err := client.Recognize(context.Background(), invoice.Attachment{URL: "https://example.com/receipt.pdf"}); err == nil {
		t.Fatal("Recognize() error = nil, want business error")
	}
}

func TestGetUsageSuccess(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", req.Method)
		}
		if !strings.Contains(req.URL.String(), "apiKey=test-key") {
			t.Errorf("request URL does not contain apiKey: %s", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"code": 200,
				"msg": "操作成功",
				"data": {
					"apiKey": "test-key",
					"balancePoints": 88,
					"availableCallCount": 88,
					"callCostPoints": 1
				}
			}`)),
			Header: make(http.Header),
		}, nil
	})}

	client, err := New("test-key", httpClient)
	if err != nil {
		t.Fatal(err)
	}

	usage, err := client.GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage() unexpected error: %v", err)
	}
	if usage.BalancePoints != 88 || usage.AvailableCallCount != 88 {
		t.Errorf("usage = %#v, want 88 points", usage)
	}
}

