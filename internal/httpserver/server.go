// Package httpserver contains the transport shell for the service.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Server exposes only infrastructure endpoints. Domain routes can be registered
// by a future application layer without coupling this package to a business system.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	version    string
}

// New builds an HTTP server with health and version endpoints.
func New(addr string, logger *slog.Logger, version string) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{logger: logger, version: version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /version", s.versionHandler)
	mux.HandleFunc("/", s.notFound)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           requestLog(logger, mux),
		ReadHeaderTimeout: 5 * 1000 * 1000 * 1000,
	}
	return s
}

// ListenAndServe starts serving until the server is stopped.
func (s *Server) ListenAndServe() error {
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "route not found"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		logger.Debug("http request", "method", r.Method, "path", r.URL.Path)
	})
}
