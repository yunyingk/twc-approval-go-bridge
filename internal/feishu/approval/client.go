package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/token"
)

const defaultInstanceEndpoint = "https://open.feishu.cn/open-apis/approval/v4/instances"

// ErrUUIDConflict indicates that the instance UUID already exists in Feishu.
var ErrUUIDConflict = errors.New("approval instance UUID already exists")

// TokenProvider defines the interface to get tenant_access_token.
type TokenProvider interface {
	TenantAccessToken(ctx context.Context) (string, error)
}

// Client handles interaction with the Feishu approval OpenAPI.
type Client struct {
	tokenProvider TokenProvider
	endpoint      string
	httpClient    *http.Client
}

// Option allows customizing Client.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(client *Client) {
		if c != nil {
			client.httpClient = c
		}
	}
}

// WithEndpoint overrides the API endpoint URL (useful for testing).
func WithEndpoint(endpoint string) Option {
	return func(client *Client) {
		if endpoint != "" {
			client.endpoint = endpoint
		}
	}
}

// NewClient creates a new Feishu approval client.
func NewClient(appID, appSecret string, opts ...Option) *Client {
	c := &Client{
		tokenProvider: token.New(appID, appSecret),
		endpoint:      defaultInstanceEndpoint,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewClientWithTokenProvider creates a client using an existing token provider.
func NewClientWithTokenProvider(tp TokenProvider, opts ...Option) *Client {
	c := &Client{
		tokenProvider: tp,
		endpoint:      defaultInstanceEndpoint,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// CreateInstance sends an instance creation request to Feishu.
func (c *Client) CreateInstance(ctx context.Context, req CreateInstanceRequest) (CreateInstanceResponse, error) {
	if strings.TrimSpace(req.ApprovalCode) == "" {
		return CreateInstanceResponse{}, fmt.Errorf("approval_code is required")
	}
	if strings.TrimSpace(req.Form) == "" {
		return CreateInstanceResponse{}, fmt.Errorf("form content is required")
	}

	tok, err := c.tokenProvider.TenantAccessToken(ctx)
	if err != nil {
		return CreateInstanceResponse{}, fmt.Errorf("acquire tenant access token: %w", err)
	}

	rawBody, err := json.Marshal(req)
	if err != nil {
		return CreateInstanceResponse{}, fmt.Errorf("marshal create instance request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return CreateInstanceResponse{}, fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return CreateInstanceResponse{}, fmt.Errorf("send create instance request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return CreateInstanceResponse{}, fmt.Errorf("read response body: %w", err)
	}

	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			InstanceCode *string `json:"instance_code"`
			InstanceLink *string `json:"instance_link"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return CreateInstanceResponse{}, fmt.Errorf("unmarshal approval response (HTTP %d): %w", resp.StatusCode, err)
	}

	if envelope.Code != 0 {
		if envelope.Code == 60012 {
			return CreateInstanceResponse{}, ErrUUIDConflict
		}
		errMsg := envelope.Msg
		if envelope.Error != nil && envelope.Error.Message != "" {
			errMsg += ": " + envelope.Error.Message
		}
		return CreateInstanceResponse{}, fmt.Errorf("feishu approval instance create failed (code %d): %s", envelope.Code, errMsg)
	}

	code := ""
	if envelope.Data.InstanceCode != nil {
		code = *envelope.Data.InstanceCode
	}
	link := ""
	if envelope.Data.InstanceLink != nil {
		link = *envelope.Data.InstanceLink
	} else if code != "" {
		link = fmt.Sprintf("https://feishu.cn/approval/s/%s", code)
	}

	return CreateInstanceResponse{
		InstanceCode: code,
		InstanceURL:  link,
	}, nil
}
