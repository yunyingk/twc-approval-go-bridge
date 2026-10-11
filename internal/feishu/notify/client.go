package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/token"
)

const defaultFeishuAPI = "https://open.feishu.cn/open-apis"

// Client delivers messages to Feishu chats and users using app identity.
type Client struct {
	baseURL       string
	httpClient    *http.Client
	tokenProvider *token.Provider
}

// Option configures Client behavior.
type Option func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(client *Client) {
		if c != nil {
			client.httpClient = c
		}
	}
}

// WithAPIBase sets a custom base URL for the Feishu open API.
func WithAPIBase(base string) Option {
	return func(client *Client) {
		if trimmed := strings.TrimRight(strings.TrimSpace(base), "/"); trimmed != "" {
			client.baseURL = trimmed
		}
	}
}

// NewClient creates a new Feishu notification client.
func NewClient(appID, appSecret string, opts ...Option) *Client {
	c := &http.Client{Timeout: 10 * time.Second}
	client := &Client{
		baseURL:    defaultFeishuAPI,
		httpClient: c,
	}
	for _, opt := range opts {
		opt(client)
	}
	client.tokenProvider = token.New(
		appID,
		appSecret,
		token.WithClient(client.httpClient),
		token.WithAPIBase(client.baseURL),
	)
	return client
}

// SendCard sends an interactive message card to a chat or user.
// receiveIDType must be "chat_id" or "open_id".
func (c *Client) SendCard(ctx context.Context, receiveIDType, receiveID, cardJSON string) (string, error) {
	receiveIDType = strings.TrimSpace(receiveIDType)
	receiveID = strings.TrimSpace(receiveID)
	if receiveID == "" {
		return "", fmt.Errorf("receiveID is required")
	}
	if receiveIDType != "chat_id" && receiveIDType != "open_id" {
		return "", fmt.Errorf("invalid receiveIDType %q: must be chat_id or open_id", receiveIDType)
	}

	tok, err := c.tokenProvider.TenantAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch access token: %w", err)
	}

	payload := map[string]any{
		"receive_id": receiveID,
		"msg_type":   "interactive",
		"content":    cardJSON,
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal message payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/im/v1/messages?receive_id_type=%s", c.baseURL, receiveIDType)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch message request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read message response: %w", err)
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return "", fmt.Errorf("decode message response (%d): %s", resp.StatusCode, string(bodyBytes))
	}
	if res.Code != 0 {
		return "", fmt.Errorf("feishu send message failed (code %d): %s", res.Code, res.Msg)
	}

	return res.Data.MessageID, nil
}

// ReplyCard replies to an existing message using an interactive card.
func (c *Client) ReplyCard(ctx context.Context, messageID, cardJSON string) (string, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", fmt.Errorf("messageID is required")
	}

	tok, err := c.tokenProvider.TenantAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch access token: %w", err)
	}

	payload := map[string]any{
		"msg_type": "interactive",
		"content":  cardJSON,
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal reply message payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/im/v1/messages/%s/reply", c.baseURL, messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create reply message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch reply message request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read reply message response: %w", err)
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return "", fmt.Errorf("decode reply message response (%d): %s", resp.StatusCode, string(bodyBytes))
	}
	if res.Code != 0 {
		return "", fmt.Errorf("feishu reply message failed (code %d): %s", res.Code, res.Msg)
	}

	return res.Data.MessageID, nil
}
