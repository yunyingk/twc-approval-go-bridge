// Package httpserver contains the transport shell for the service.
package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/audit"
	"github.com/yunyingk/twc-approval-go-bridge/internal/version"
)

// ReadyChecker reports whether the application's runtime dependencies are operational.
type ReadyChecker func(context.Context) error

// Server starts with infrastructure endpoints. Domain routes can be registered
// by the application layer without coupling this package to a business system.
type Server struct {
	httpServer     *http.Server
	mux            *http.ServeMux
	logger         *slog.Logger
	version        string
	startTime      time.Time
	statusProvider StatusProvider
	readyChecker   ReadyChecker
	password       string
	logBuffer      *LogBuffer
	tokenManager   *TokenManager
	publicBaseURL  string
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
	mux.HandleFunc("GET /api/audit", s.auditAPI)
	mux.HandleFunc("GET /api/audit/timeline", s.auditTimelineAPI)
	mux.HandleFunc("GET /api/system-logs", s.systemLogsAPI)
	mux.HandleFunc("GET /api/agent/diagnose", s.agentDiagnoseAPI)
	mux.HandleFunc("GET /api/agent/inspect", s.agentInspectAPI)
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

// SetLogBuffer attaches a circular log buffer for live system logs inspection.
func (s *Server) SetLogBuffer(b *LogBuffer) {
	s.logBuffer = b
}

// SetTokenManager configures the short-lived magic token manager.
func (s *Server) SetTokenManager(tm *TokenManager) {
	s.tokenManager = tm
}

// SetPublicBaseURL sets the external public ingress base URL.
func (s *Server) SetPublicBaseURL(url string) {
	s.publicBaseURL = strings.TrimRight(strings.TrimSpace(url), "/")
}

func (s *Server) tokenValue() string {
	if s.password == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("twc_dash_secret:" + s.password))
	return hex.EncodeToString(sum[:])
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	if s.password == "" && (s.tokenManager == nil || len(s.tokenManager.ActiveTokens()) == 0) {
		return true
	}
	// 1. Check Cookie
	if cookie, err := r.Cookie("twc_dash_token"); err == nil && cookie != nil {
		if s.password != "" && cookie.Value == s.tokenValue() {
			return true
		}
		if s.tokenManager != nil && s.tokenManager.IsValid(cookie.Value) {
			return true
		}
	}
	// 2. Check query param ?token=
	if token := r.URL.Query().Get("token"); token != "" {
		if s.password != "" && (token == s.tokenValue() || token == s.password) {
			return true
		}
		if s.tokenManager != nil && s.tokenManager.IsValid(token) {
			return true
		}
	}
	// 3. Check X-Dashboard-Token header
	if token := r.Header.Get("X-Dashboard-Token"); token != "" {
		if s.password != "" && (token == s.tokenValue() || token == s.password) {
			return true
		}
		if s.tokenManager != nil && s.tokenManager.IsValid(token) {
			return true
		}
	}
	// 4. Check Basic Auth (兼容工具/脚本)
	if _, pass, ok := r.BasicAuth(); ok && s.password != "" && pass == s.password {
		return true
	}
	// 5. Check Bearer token
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		if s.password != "" && (token == s.tokenValue() || token == s.password) {
			return true
		}
		if s.tokenManager != nil && s.tokenManager.IsValid(token) {
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
	if cookie, err := r.Cookie("twc_dash_token"); err == nil && cookie != nil && s.tokenManager != nil {
		s.tokenManager.Revoke(cookie.Value)
	}
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

// SetReadyChecker attaches a dynamic readiness probe to the server.
func (s *Server) SetReadyChecker(c ReadyChecker) {
	s.readyChecker = c
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

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.readyChecker != nil {
		if err := s.readyChecker(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"reason": err.Error(),
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) versionHandler(w http.ResponseWriter, _ *http.Request) {
	info := version.Get()
	if s.version != "" && s.version != "dev" && s.version != version.Version {
		info.Version = s.version
	}
	writeJSON(w, http.StatusOK, info)
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
	info := version.Get()
	ver := s.version
	if ver == "" {
		ver = info.Version
	}
	writeJSON(w, http.StatusOK, Status{
		Version:       ver,
		GitCommit:     info.GitCommit,
		BuildDate:     info.BuildDate,
		HTTPAddr:      s.httpServer.Addr,
		StartTime:     s.startTime,
		Uptime:        uptime.String(),
		UptimeSeconds: int64(uptime.Seconds()),
		FeishuState:   "disabled",
	})
}

func (s *Server) auditAPI(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	filter := audit.QueryFilter{
		RecordID: r.URL.Query().Get("record_id"),
		Type:     r.URL.Query().Get("type"),
		Operator: r.URL.Query().Get("user"),
		Limit:    limit,
	}
	entries, total, err := audit.Default().Query(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":   total,
		"count":   len(entries),
		"entries": entries,
	})
}

func (s *Server) auditTimelineAPI(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	recordID := r.URL.Query().Get("record_id")
	if recordID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "record_id parameter is required"})
		return
	}
	timeline, err := audit.Default().Timeline(r.Context(), recordID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record_id": recordID,
		"count":     len(timeline),
		"timeline":  timeline,
	})
}

func (s *Server) systemLogsAPI(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	sinceID, _ := strconv.ParseInt(r.URL.Query().Get("since_id"), 10, 64)
	level := r.URL.Query().Get("level")
	if level == "" {
		level = r.URL.Query().Get("min_level")
	}
	keyword := r.URL.Query().Get("q")
	if keyword == "" {
		keyword = r.URL.Query().Get("keyword")
	}
	order := r.URL.Query().Get("order")
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))

	var entries []SystemLogEntry
	if s.logBuffer != nil {
		entries = s.logBuffer.QueryExtended(LogFilterOptions{
			Limit:    limit,
			MinLevel: level,
			SinceID:  sinceID,
			Keyword:  keyword,
			Order:    order,
		})
	}

	if format == "text" || format == "plain" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		for _, e := range entries {
			fmt.Fprintln(w, e.FormatLine())
		}
		return
	}

	var lastID int64
	if len(entries) > 0 {
		lastID = entries[len(entries)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(entries),
		"last_id": lastID,
		"logs":    entries,
	})
}

// AgentDiagnosticReport represents a consolidated health and diagnostic snapshot for AI agents.
type AgentDiagnosticReport struct {
	OverallHealth string           `json:"overall_health"` // "HEALTHY", "DEGRADED", "UNHEALTHY"
	Issues        []string         `json:"issues,omitempty"`
	Status        Status           `json:"status"`
	RecentErrors  []SystemLogEntry `json:"recent_errors,omitempty"`
	RecentAudits  []audit.Entry    `json:"recent_audits,omitempty"`
	GeneratedAt   string           `json:"generated_at"`
}

// AgentInspectReport represents an end-to-end trace snapshot for a specific record.
type AgentInspectReport struct {
	RecordID    string           `json:"record_id"`
	LastAction  string           `json:"last_action"`
	HasError    bool             `json:"has_error"`
	LastError   string           `json:"last_error,omitempty"`
	Timeline    []audit.Entry    `json:"timeline"`
	RelatedLogs []SystemLogEntry `json:"related_logs,omitempty"`
}

func (s *Server) agentDiagnoseAPI(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	// 1. Gather live status
	var st Status
	if s.statusProvider != nil {
		st = s.statusProvider()
	} else {
		info := version.Get()
		ver := s.version
		if ver == "" {
			ver = info.Version
		}
		st = Status{
			Version:       ver,
			GitCommit:     info.GitCommit,
			BuildDate:     info.BuildDate,
			HTTPAddr:      s.httpServer.Addr,
			StartTime:     s.startTime,
			Uptime:        time.Since(s.startTime).Truncate(time.Second).String(),
			UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
		}
	}

	// 2. Scan recent Warn/Error logs
	var recentErrors []SystemLogEntry
	if s.logBuffer != nil {
		recentErrors = s.logBuffer.QueryExtended(LogFilterOptions{
			MinLevel: "WARN",
			Limit:    10,
			Order:    "desc",
		})
	}

	// 3. Scan recent audit events
	recentAudits, _, _ := audit.Default().Query(r.Context(), audit.QueryFilter{Limit: 10})

	// 4. Derive Overall Health
	health := "HEALTHY"
	var issues []string
	if st.FeishuState != "" && st.FeishuState != "ready" && st.FeishuState != "disabled" {
		health = "DEGRADED"
		issues = append(issues, fmt.Sprintf("Feishu WebSocket state is %s", st.FeishuState))
	}
	if st.Anyreceipt != nil && st.Anyreceipt.Configured && st.Anyreceipt.AvailableCallCount <= 0 {
		health = "DEGRADED"
		issues = append(issues, "Anyreceipt OCR balance points are exhausted")
	}
	for _, l := range recentErrors {
		if strings.EqualFold(l.Level, "ERROR") {
			health = "DEGRADED"
			break
		}
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "markdown" || format == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintln(w, "# TWC Approval Bridge - AI Agent 诊断快照")
		fmt.Fprintf(w, "**总体健康状态**: %s\n", health)
		fmt.Fprintf(w, "**快照时间**: %s\n\n", FormatCST(time.Now()))

		if len(issues) > 0 {
			fmt.Fprintln(w, "### ⚠️ 潜在健康隐患")
			for _, issue := range issues {
				fmt.Fprintf(w, "- %s\n", issue)
			}
			fmt.Fprintln(w)
		}

		fmt.Fprintln(w, "### 1. 运行时指标")
		fmt.Fprintf(w, "- **服务版本**: %s (Commit: %s, Built: %s)\n", st.Version, st.GitCommit, st.BuildDate)
		fmt.Fprintf(w, "- **运行时长**: %s\n", st.Uptime)
		fmt.Fprintf(w, "- **飞书长连接**: %s (已接收 %d 个事件)\n", st.FeishuState, st.FeishuEvents)
		if st.Anyreceipt != nil && st.Anyreceipt.Configured {
			fmt.Fprintf(w, "- **Anyreceipt OCR**: 剩余 %d 点 (约可调用 %d 次)\n", st.Anyreceipt.BalancePoints, st.Anyreceipt.AvailableCallCount)
		}
		if st.ReceiptProvider != "" || st.ReviewProvider != "" {
			fmt.Fprintf(w, "- **提供方编排**: 识别=%s | 审核=%s (%s)\n", st.ReceiptProvider, st.ReviewProvider, st.ReviewTrigger)
		}

		fmt.Fprintln(w, "\n### 2. 异常雷达 (最近 WARN/ERROR 日志)")
		if len(recentErrors) == 0 {
			fmt.Fprintln(w, "- 当前内存日志缓冲区中无 WARN/ERROR 异常记录")
		} else {
			for _, l := range recentErrors {
				fmt.Fprintf(w, "- %s\n", l.FormatLine())
			}
		}

		fmt.Fprintln(w, "\n### 3. 最近业务审计事件")
		if len(recentAudits) == 0 {
			fmt.Fprintln(w, "- 暂无近期审计事件")
		} else {
			for _, a := range recentAudits {
				fmt.Fprintf(w, "- %s [%s] %s\n", FormatCST(a.Timestamp), a.Type, a.Action)
			}
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"overall_health": health,
		"issues":         issues,
		"status":         st,
		"recent_errors":  recentErrors,
		"recent_audits":  recentAudits,
		"generated_at":   FormatCST(time.Now()),
	})
}

func (s *Server) agentInspectAPI(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	recordID := strings.TrimSpace(r.URL.Query().Get("record_id"))
	if recordID == "" {
		recordID = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if recordID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "record_id is required"})
		return
	}

	// 1. Audit timeline
	timeline, err := audit.Default().Timeline(r.Context(), recordID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// 2. Correlated logs from buffer
	var relatedLogs []SystemLogEntry
	if s.logBuffer != nil {
		relatedLogs = s.logBuffer.QueryExtended(LogFilterOptions{
			Keyword: recordID,
			Limit:   50,
			Order:   "asc",
		})
	}

	// 3. Infer summary & bottlenecks
	lastAction := "未发生任何流转"
	hasError := false
	var lastErrorMsg string
	if len(timeline) > 0 {
		lastEntry := timeline[len(timeline)-1]
		lastAction = fmt.Sprintf("[%s] %s (%s)", lastEntry.Type, lastEntry.Action, FormatCST(lastEntry.Timestamp))
		if strings.Contains(lastEntry.Action, "失败") || strings.Contains(lastEntry.Action, "跳过") {
			hasError = true
			lastErrorMsg = lastEntry.Action
		}
	}
	for _, l := range relatedLogs {
		if strings.EqualFold(l.Level, "ERROR") {
			hasError = true
			lastErrorMsg = l.Message
		}
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "markdown" || format == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "# 单据调查快照: %s\n\n", recordID)
		fmt.Fprintf(w, "- **最新流转**: %s\n", lastAction)
		if hasError {
			fmt.Fprintf(w, "- **⚠️ 异常提示**: %s\n", lastErrorMsg)
		}
		fmt.Fprintf(w, "\n## 审计时间线 (%d 步)\n", len(timeline))
		if len(timeline) == 0 {
			fmt.Fprintln(w, "- 审计存储中尚未发现该记录 ID 的流转时间线")
		} else {
			for i, entry := range timeline {
				fmt.Fprintf(w, "%d. %s [%s] %s\n", i+1, FormatCST(entry.Timestamp), entry.Type, entry.Action)
			}
		}
		if len(relatedLogs) > 0 {
			fmt.Fprintf(w, "\n## 关联日志行 (%d 条)\n", len(relatedLogs))
			for _, l := range relatedLogs {
				fmt.Fprintf(w, "- %s\n", l.FormatLine())
			}
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"record_id":    recordID,
		"last_action":  lastAction,
		"has_error":    hasError,
		"last_error":   lastErrorMsg,
		"timeline":     timeline,
		"related_logs": relatedLogs,
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
