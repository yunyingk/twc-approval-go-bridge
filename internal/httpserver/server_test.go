package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	s := New(":0", slog.Default(), "test")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q, want health response", got)
	}
}

func TestUnknownRoute(t *testing.T) {
	s := New(":0", slog.Default(), "test")
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	res := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotFound)
	}
}

func TestCallbackSecretIsNotWrittenToRequestLog(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handler := requestLog(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/seal/callback/sensitive-token", nil))
	if strings.Contains(output.String(), "sensitive-token") || !strings.Contains(output.String(), "[redacted]") {
		t.Fatal("callback credential leaked into request log")
	}
}
