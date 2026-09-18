package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
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
