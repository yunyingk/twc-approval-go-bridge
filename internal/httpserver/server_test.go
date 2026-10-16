package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/audit"
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

func TestPasswordLockScreenAndCookieAuth(t *testing.T) {
	s := New(":0", slog.Default(), "test")
	s.SetPassword("mysecret")

	// 1. Visit / without auth -> returns 200 with lock screen, containing password input, NO username input
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("lock screen status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "监控看板验证") || !strings.Contains(body, "type=\"password\"") {
		t.Fatalf("lock screen does not contain password form: %s", body)
	}
	if strings.Contains(body, "username") {
		t.Fatalf("lock screen should not ask for username")
	}

	// 2. Visit /api/status without auth -> returns 401 JSON, and NO WWW-Authenticate header
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	resAPI := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(resAPI, reqAPI)

	if resAPI.Code != http.StatusUnauthorized {
		t.Fatalf("status API unauthorized status = %d, want 401", resAPI.Code)
	}
	if resAPI.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("status API should not set WWW-Authenticate header to avoid browser dialog")
	}

	// 3. POST /login with wrong password -> returns lock screen with error
	formWrong := strings.NewReader("password=wrongpwd")
	reqLoginWrong := httptest.NewRequest(http.MethodPost, "/login", formWrong)
	reqLoginWrong.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resLoginWrong := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(resLoginWrong, reqLoginWrong)

	if !strings.Contains(resLoginWrong.Body.String(), "密码错误") {
		t.Fatalf("expected error message on wrong password, got: %s", resLoginWrong.Body.String())
	}

	// 4. POST /login with correct password -> sets cookie and redirects to /
	formCorrect := strings.NewReader("password=mysecret")
	reqLoginCorrect := httptest.NewRequest(http.MethodPost, "/login", formCorrect)
	reqLoginCorrect.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resLoginCorrect := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(resLoginCorrect, reqLoginCorrect)

	if resLoginCorrect.Code != http.StatusSeeOther {
		t.Fatalf("login redirect status = %d, want 303", resLoginCorrect.Code)
	}
	cookies := resLoginCorrect.Result().Cookies()
	var authCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "twc_dash_token" {
			authCookie = c
			break
		}
	}
	if authCookie == nil || authCookie.Value == "" {
		t.Fatal("expected twc_dash_token cookie to be set")
	}

	// 5. Visit / with valid cookie -> returns 200 with full dashboard
	reqAuthed := httptest.NewRequest(http.MethodGet, "/", nil)
	reqAuthed.AddCookie(authCookie)
	resAuthed := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(resAuthed, reqAuthed)

	if resAuthed.Code != http.StatusOK {
		t.Fatalf("authed dashboard status = %d, want 200", resAuthed.Code)
	}
	if !strings.Contains(resAuthed.Body.String(), "海外易商卡报销网桥") || !strings.Contains(resAuthed.Body.String(), "退出登录") {
		t.Fatalf("dashboard missing expected contents: %s", resAuthed.Body.String())
	}

	// 6. Visit /logout -> clears cookie
	reqLogout := httptest.NewRequest(http.MethodGet, "/logout", nil)
	resLogout := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(resLogout, reqLogout)

	logoutCookies := resLogout.Result().Cookies()
	var clearedCookie *http.Cookie
	for _, c := range logoutCookies {
		if c.Name == "twc_dash_token" {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil || clearedCookie.MaxAge >= 0 {
		t.Fatal("logout should clear twc_dash_token cookie with MaxAge < 0")
	}
}

func TestVersionEndpoint(t *testing.T) {
	s := New(":0", slog.Default(), "0.3.1")
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	res := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("version status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, `"version":"0.3.1"`) {
		t.Fatalf("expected version 0.3.1 in response, got: %s", body)
	}
}

func TestAuditEndpoints(t *testing.T) {
	s := New(":0", slog.Default(), "0.3.1")

	// 1. GET /api/audit
	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"entries"`) {
		t.Fatalf("audit response missing entries key: %s", res.Body.String())
	}

	// 2. GET /api/audit/timeline missing record_id -> 400
	req2 := httptest.NewRequest(http.MethodGet, "/api/audit/timeline", nil)
	res2 := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res2, req2)
	if res2.Code != http.StatusBadRequest {
		t.Fatalf("timeline missing param status = %d, want 400", res2.Code)
	}

	// 3. GET /api/audit/timeline with record_id -> 200
	req3 := httptest.NewRequest(http.MethodGet, "/api/audit/timeline?record_id=rec_test123", nil)
	res3 := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res3, req3)
	if res3.Code != http.StatusOK {
		t.Fatalf("timeline status = %d, want 200", res3.Code)
	}
}

func TestSystemLogsAPI(t *testing.T) {
	buf := NewLogBuffer(100)
	base := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewBufferedHandler(base, buf))

	logger.Info("server starting", "port", 8080)
	logger.Warn("disk space warning", "free_mb", 500)
	logger.Error("connection timeout", "target", "db")

	s := New(":0", slog.Default(), "0.3.2")
	s.SetLogBuffer(buf)

	// 1. Unauthenticated request when password is set -> 401
	s.SetPassword("supersecret")
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/system-logs", nil)
	resAuth := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(resAuth, reqAuth)
	if resAuth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resAuth.Code)
	}

	// 2. Clear password, query all logs -> 200 with 3 entries
	s.SetPassword("")
	req := httptest.NewRequest(http.MethodGet, "/api/system-logs", nil)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("system-logs status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "server starting") || !strings.Contains(body, "connection timeout") {
		t.Fatalf("expected log messages in response, got: %s", body)
	}

	// 3. Filter by level=ERROR -> only connection timeout
	reqErr := httptest.NewRequest(http.MethodGet, "/api/system-logs?level=ERROR", nil)
	resErr := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(resErr, reqErr)
	bodyErr := resErr.Body.String()
	if !strings.Contains(bodyErr, "connection timeout") || strings.Contains(bodyErr, "server starting") {
		t.Fatalf("expected only ERROR logs, got: %s", bodyErr)
	}
}

func TestFormatCSTAndDashboardEventTypes(t *testing.T) {
	// 1. FormatCST
	fixedUTC := time.Date(2026, 10, 12, 2, 7, 44, 0, time.UTC)
	gotCST := FormatCST(fixedUTC)
	wantCST := "2026-10-12 10:07:44 (UTC+8)"
	if gotCST != wantCST {
		t.Fatalf("FormatCST = %q, want %q", gotCST, wantCST)
	}

	zeroTime := time.Time{}
	if gotZero := FormatCST(zeroTime); gotZero != "未记录" {
		t.Fatalf("FormatCST(zero) = %q, want %q", gotZero, "未记录")
	}

	// 2. Dashboard rendering with multiple event types
	s := New(":0", slog.Default(), "0.3.2")
	s.SetStatusProvider(func() Status {
		return Status{
			Version:          "0.3.2",
			StartTime:        fixedUTC,
			FeishuState:      "ready",
			FeishuConnected:  gotCST,
			FeishuEventTypes: []string{"drive.file.bitable_record_changed_v1", "im.message.receive_v1"},
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "2026-10-12 10:07:44 (UTC+8)") {
		t.Fatalf("dashboard missing CST start time: %s", body)
	}
	if !strings.Contains(body, "drive.file.bitable_record_changed_v1") || !strings.Contains(body, "im.message.receive_v1") {
		t.Fatalf("dashboard missing multiple event types: %s", body)
	}
}

func TestReadyEndpointWithChecker(t *testing.T) {
	s := New(":0", slog.Default(), "test")

	// 1. Without checker, defaults to ready
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}

	// 2. With failing checker, returns 503
	s.SetReadyChecker(func(ctx context.Context) error {
		return errors.New("feishu disconnected")
	})
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.Code)
	}
	if !strings.Contains(res.Body.String(), "feishu disconnected") {
		t.Fatalf("expected error reason in body, got %s", res.Body.String())
	}

	// 3. With passing checker, returns 200
	s.SetReadyChecker(func(ctx context.Context) error {
		return nil
	})
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
}

func TestAgentDiagnoseAPI_JSONAndMarkdown(t *testing.T) {
	s := New(":0", slog.Default(), "0.3.3")
	s.SetPassword("secret123")

	buffer := NewLogBuffer(100)
	buffer.Add(time.Now(), slog.LevelWarn, "something feels slow", map[string]any{"component": "ocr"})
	s.SetLogBuffer(buffer)

	// 1. Unauthorized request
	req := httptest.NewRequest(http.MethodGet, "/api/agent/diagnose", nil)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", res.Code)
	}

	// 2. Authorized JSON request via Bearer token
	req = httptest.NewRequest(http.MethodGet, "/api/agent/diagnose", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", res.Code, res.Body.String())
	}
	var report AgentDiagnosticReport
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to decode JSON report: %v", err)
	}
	if report.OverallHealth != "HEALTHY" {
		t.Errorf("expected HEALTHY, got %s", report.OverallHealth)
	}
	if len(report.RecentErrors) != 1 || report.RecentErrors[0].Message != "something feels slow" {
		t.Errorf("unexpected recent errors: %#v", report.RecentErrors)
	}

	// 3. Authorized Markdown request via Bearer token
	req = httptest.NewRequest(http.MethodGet, "/api/agent/diagnose?format=markdown", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", res.Code)
	}
	md := res.Body.String()
	if !strings.Contains(md, "# TWC Approval Bridge - AI Agent 诊断快照") {
		t.Errorf("expected markdown header, got: %s", md)
	}
	if !strings.Contains(md, "something feels slow") {
		t.Errorf("expected warn log in markdown, got: %s", md)
	}
}

func TestAgentInspectAPI_JSONAndMarkdown(t *testing.T) {
	s := New(":0", slog.Default(), "0.3.3")
	s.SetPassword("secret123")

	buffer := NewLogBuffer(100)
	buffer.Add(time.Now(), slog.LevelInfo, "downloading receipt attachment", map[string]any{"record_id": "rec_inspect_001"})
	s.SetLogBuffer(buffer)

	// Record an audit entry
	audit.Log(audit.Entry{
		Timestamp: time.Now(),
		Type:      audit.TypeOCRCompleted,
		RecordID:  "rec_inspect_001",
		Action:    "发票台账回写成功",
	})

	// 1. Missing record_id returns 400
	req := httptest.NewRequest(http.MethodGet, "/api/agent/inspect", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", res.Code)
	}

	// 2. Query JSON inspect
	req = httptest.NewRequest(http.MethodGet, "/api/agent/inspect?record_id=rec_inspect_001", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", res.Code)
	}
	var data map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &data); err != nil {
		t.Fatalf("decode inspect json: %v", err)
	}
	if data["record_id"] != "rec_inspect_001" {
		t.Errorf("unexpected record_id: %v", data["record_id"])
	}
	logs, ok := data["related_logs"].([]any)
	if !ok || len(logs) == 0 {
		t.Errorf("expected related logs in inspect data")
	}

	// 3. Query Markdown inspect
	req = httptest.NewRequest(http.MethodGet, "/api/agent/inspect?record_id=rec_inspect_001&format=markdown", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for markdown inspect, got %d", res.Code)
	}
	md := res.Body.String()
	if !strings.Contains(md, "# 单据调查快照: rec_inspect_001") {
		t.Errorf("unexpected inspect markdown header: %s", md)
	}
	if !strings.Contains(md, "downloading receipt attachment") {
		t.Errorf("expected correlated log in markdown: %s", md)
	}
}

func TestSystemLogsAPI_ExtendedFiltering(t *testing.T) {
	s := New(":0", slog.Default(), "0.3.3")
	buffer := NewLogBuffer(100)
	t0 := time.Now()
	buffer.Add(t0.Add(1*time.Second), slog.LevelInfo, "starting service", nil)
	buffer.Add(t0.Add(2*time.Second), slog.LevelWarn, "token expiring soon", map[string]any{"token": "tok_1"})
	buffer.Add(t0.Add(3*time.Second), slog.LevelError, "failed to call anyreceipt", map[string]any{"err_code": 500})
	s.SetLogBuffer(buffer)

	// 1. Keyword search
	req := httptest.NewRequest(http.MethodGet, "/api/system-logs?q=anyreceipt", nil)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	var data map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &data)
	if count, ok := data["count"].(float64); !ok || count != 1 {
		t.Errorf("expected count 1, got %v", data["count"])
	}

	// 2. Order desc (latest first)
	req = httptest.NewRequest(http.MethodGet, "/api/system-logs?order=desc&limit=2", nil)
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	_ = json.Unmarshal(res.Body.Bytes(), &data)
	logs := data["logs"].([]any)
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
	firstLog := logs[0].(map[string]any)
	if firstLog["message"] != "failed to call anyreceipt" {
		t.Errorf("expected latest log first, got %s", firstLog["message"])
	}

	// 3. Format text
	req = httptest.NewRequest(http.MethodGet, "/api/system-logs?format=text&limit=1&order=desc", nil)
	res = httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	text := res.Body.String()
	if !strings.Contains(text, "[ERROR] failed to call anyreceipt") {
		t.Errorf("unexpected text format: %s", text)
	}
}



