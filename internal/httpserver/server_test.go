package httpserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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


