// Package httpserver contains the transport shell for the service.
package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Server starts with infrastructure endpoints. Domain routes can be registered
// by the application layer without coupling this package to a business system.
type Server struct {
	httpServer     *http.Server
	mux            *http.ServeMux
	logger         *slog.Logger
	version        string
	startTime      time.Time
	statusProvider StatusProvider
	password       string
}

// New builds an HTTP server with health and version endpoints.
func New(addr string, logger *slog.Logger, version string) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{logger: logger, version: version, startTime: time.Now()}
	mux := http.NewServeMux()
	s.mux = mux
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /version", s.versionHandler)
	mux.HandleFunc("GET /api/status", s.statusAPI)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /logout", s.handleLogout)
	mux.HandleFunc("/", s.renderDashboard)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           requestLog(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// SetPassword configures password protection for the dashboard and status API.
func (s *Server) SetPassword(pwd string) {
	s.password = pwd
}

func (s *Server) tokenValue() string {
	if s.password == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("twc_dash_secret:" + s.password))
	return hex.EncodeToString(sum[:])
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	if s.password == "" {
		return true
	}
	// 1. Check Cookie
	if cookie, err := r.Cookie("twc_dash_token"); err == nil && cookie != nil {
		if cookie.Value == s.tokenValue() {
			return true
		}
	}
	// 2. Check X-Dashboard-Token header
	if token := r.Header.Get("X-Dashboard-Token"); token != "" {
		if token == s.tokenValue() || token == s.password {
			return true
		}
	}
	// 3. Check Basic Auth (兼容工具/脚本)
	if _, pass, ok := r.BasicAuth(); ok && pass == s.password {
		return true
	}
	// 4. Check Bearer token
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == s.tokenValue() || token == s.password {
			return true
		}
	}
	return false
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	pwd := r.FormValue("password")
	if pwd == s.password {
		http.SetCookie(w, &http.Cookie{
			Name:     "twc_dash_token",
			Value:    s.tokenValue(),
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   30 * 24 * 3600, // 30 days
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLockScreen(w, r, "密码错误，请核对后重试")
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "twc_dash_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// SetStatusProvider attaches a live status reporter to the server dashboard.
func (s *Server) SetStatusProvider(p StatusProvider) {
	s.statusProvider = p
}

// Register mounts an optional domain adapter before ListenAndServe.
func (s *Server) Register(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
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

func (s *Server) statusAPI(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if s.statusProvider != nil {
		writeJSON(w, http.StatusOK, s.statusProvider())
		return
	}
	uptime := time.Since(s.startTime).Truncate(time.Second)
	writeJSON(w, http.StatusOK, Status{
		Version:       s.version,
		HTTPAddr:      s.httpServer.Addr,
		StartTime:     s.startTime,
		Uptime:        uptime.String(),
		UptimeSeconds: int64(uptime.Seconds()),
		FeishuState:   "disabled",
	})
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
		path := r.URL.Path
		if strings.HasPrefix(path, "/seal/callback/") && path != "/seal/callback/mock" {
			path = "/seal/callback/[redacted]"
		}
		logger.Debug("http request", "method", r.Method, "path", path)
	})
}
