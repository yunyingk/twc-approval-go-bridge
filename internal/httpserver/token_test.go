package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTokenManager_Lifecycle(t *testing.T) {
	tm := NewTokenManager(100 * time.Millisecond)

	tok := tm.Generate("ou_user123", "测试用户", 100*time.Millisecond)
	if !strings.HasPrefix(tok.Token, "twc_m_") {
		t.Fatalf("unexpected token prefix: %s", tok.Token)
	}
	if tok.UserID != "ou_user123" || tok.UserName != "测试用户" {
		t.Fatalf("unexpected user info: %+v", tok)
	}

	// Validate immediately
	entry, remaining, ok := tm.Validate(tok.Token)
	if !ok || entry == nil {
		t.Fatalf("expected token to be valid")
	}
	if remaining <= 0 {
		t.Fatalf("expected positive remaining TTL")
	}
	if entry.UsedCount != 1 {
		t.Fatalf("expected used count 1, got %d", entry.UsedCount)
	}

	// Wait for expiration
	time.Sleep(120 * time.Millisecond)
	_, _, ok = tm.Validate(tok.Token)
	if ok {
		t.Fatalf("expected token to be expired")
	}
	if tm.IsValid(tok.Token) {
		t.Fatalf("expected IsValid to report false")
	}
}

func TestTokenManager_Revoke(t *testing.T) {
	tm := NewTokenManager(2 * time.Hour)
	tok := tm.Generate("ou_user1", "User", 0)

	if !tm.IsValid(tok.Token) {
		t.Fatalf("expected token to be valid")
	}

	tm.Revoke(tok.Token)
	if tm.IsValid(tok.Token) {
		t.Fatalf("expected token to be revoked")
	}
}

func TestServer_MagicTokenAuth(t *testing.T) {
	s := New("127.0.0.1:0", nil, "0.3.2")
	s.SetPassword("twc888")
	tm := NewTokenManager(2 * time.Hour)
	s.SetTokenManager(tm)

	tok := tm.Generate("ou_user1", "谢子豪", 2*time.Hour)

	// 1. Visit /?token=... -> expect 303 redirect with Set-Cookie
	req := httptest.NewRequest("GET", "/?token="+tok.Token, nil)
	w := httptest.NewRecorder()
	s.renderDashboard(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}
	cookies := resp.Cookies()
	var dashCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "twc_dash_token" {
			dashCookie = c
			break
		}
	}
	if dashCookie == nil {
		t.Fatalf("expected twc_dash_token cookie in response")
	}
	if dashCookie.Value != tok.Token {
		t.Fatalf("expected cookie value to match magic token")
	}

	// 2. Subsequent request with the issued cookie to / -> should render dashboard
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(dashCookie)
	w2 := httptest.NewRecorder()
	s.renderDashboard(w2, req2)
	resp2 := w2.Result()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for dashboard, got %d", resp2.StatusCode)
	}
	body := w2.Body.String()
	if !strings.Contains(body, "nav-tabs") {
		t.Fatalf("expected dashboard HTML with nav-tabs in response, got: %s", body[:min(len(body), 200)])
	}

	// 3. API request with Bearer token
	reqAPI := httptest.NewRequest("GET", "/api/system-logs", nil)
	reqAPI.Header.Set("Authorization", "Bearer "+tok.Token)
	wAPI := httptest.NewRecorder()
	s.systemLogsAPI(wAPI, reqAPI)
	if wAPI.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for API with Bearer token, got %d", wAPI.Result().StatusCode)
	}

	// 4. Test expired token visiting /?token=... -> should show lock screen with expiration message
	expiredTok := tm.Generate("ou_user2", "User2", 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	reqExp := httptest.NewRequest("GET", "/?token="+expiredTok.Token, nil)
	wExp := httptest.NewRecorder()
	s.renderDashboard(wExp, reqExp)
	if !strings.Contains(wExp.Body.String(), "已失效或已过期") {
		t.Fatalf("expected expiration message on lock screen")
	}
}
