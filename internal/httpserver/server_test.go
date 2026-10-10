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

