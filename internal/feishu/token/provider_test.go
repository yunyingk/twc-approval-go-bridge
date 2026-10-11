package token

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestProviderCachesTokenUntilExpiry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":                0,
			"msg":                 "ok",
			"tenant_access_token": "token_mock_123",
			"expire":              7200,
		})
	}))
	defer server.Close()

	p := New("app_id", "app_secret", WithClient(server.Client()), WithAPIBase(server.URL))
	ctx := context.Background()

	// 10 concurrent requests
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := p.TenantAccessToken(ctx)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tok != "token_mock_123" {
				t.Errorf("expected token_mock_123, got %q", tok)
			}
		}()
	}
	wg.Wait()

	// Should only make 1 actual HTTP call due to caching
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 HTTP call, got %d", calls.Load())
	}
}

func TestProviderRejectsEmptyCredentials(t *testing.T) {
	p := New("", "")
	if _, err := p.TenantAccessToken(context.Background()); err == nil {
		t.Fatal("expected error for empty credentials")
	}
}
