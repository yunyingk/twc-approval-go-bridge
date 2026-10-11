package token

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultAPIBase = "https://open.feishu.cn/open-apis"

// Provider fetches and caches Feishu tenant_access_token with automatic expiry renewal.
type Provider struct {
	appID     string
	appSecret string
	apiBase   string
	client    *http.Client

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// Option configures Provider.
type Option func(*Provider)

// WithClient configures custom HTTP client.
func WithClient(c *http.Client) Option {
	return func(p *Provider) {
		if c != nil {
			p.client = c
		}
	}
}

// WithAPIBase configures custom Feishu API base URL.
func WithAPIBase(base string) Option {
	return func(p *Provider) {
		if trimmed := strings.TrimRight(strings.TrimSpace(base), "/"); trimmed != "" {
			p.apiBase = trimmed
		}
	}
}

// New creates a new token Provider.
func New(appID, appSecret string, opts ...Option) *Provider {
	p := &Provider{
		appID:     strings.TrimSpace(appID),
		appSecret: strings.TrimSpace(appSecret),
		apiBase:   defaultAPIBase,
		client:    &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// APIBase returns current API base URL.
func (p *Provider) APIBase() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiBase
}

// SetAPIBase updates API base URL and clears token if base changed.
func (p *Provider) SetAPIBase(base string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmed != "" && trimmed != p.apiBase {
		p.apiBase = trimmed
		p.token = ""
		p.expiresAt = time.Time{}
	}
}

// TenantAccessToken returns a valid tenant_access_token, refreshing it if expired or missing.
func (p *Provider) TenantAccessToken(ctx context.Context) (string, error) {
	if p.appID == "" || p.appSecret == "" {
		return "", fmt.Errorf("feishu credentials (app_id, app_secret) are required")
	}

	p.mu.RLock()
	if p.token != "" && time.Now().Before(p.expiresAt) {
		token := p.token
		p.mu.RUnlock()
		return token, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-checked locking
	if p.token != "" && time.Now().Before(p.expiresAt) {
		return p.token, nil
	}

	body, err := json.Marshal(map[string]string{
		"app_id":     p.appID,
		"app_secret": p.appSecret,
	})
	if err != nil {
		return "", err
	}

	endpoint := p.apiBase + "/auth/v3/tenant_access_token/internal"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request feishu tenant access token: %w", err)
	}
	defer resp.Body.Close()

	var data struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("decode feishu tenant access token response: %w", err)
	}
	if data.Code != 0 || data.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu auth failed (code %d): %s", data.Code, data.Msg)
	}

	ttl := data.Expire
	if ttl <= 0 {
		ttl = 7200
	}
	// Reserve safe buffer (e.g. 5 minutes before actual expiry, but at least 30s)
	buffer := 300
	if ttl <= buffer {
		buffer = ttl / 2
	}
	p.token = data.TenantAccessToken
	p.expiresAt = time.Now().Add(time.Duration(ttl-buffer) * time.Second)

	return p.token, nil
}
