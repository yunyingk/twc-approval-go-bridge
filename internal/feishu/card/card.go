// Package card constructs and sends Feishu interactive message cards.
package card

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const feishuAPI = "https://open.feishu.cn/open-apis"

// TransactionNotice carries details from a credit card transaction row
// to render a receipt supplementary request card.
type TransactionNotice struct {
	TransactionID   string `json:"transaction_id"`   // 交易流水号
	TransactionTime string `json:"transaction_time"` // 交易日期
	Merchant        string `json:"merchant"`         // 商户名称
	BookedAmount    string `json:"booked_amount"`    // 结算金额 (如 "CNY 1462.55")
	OriginalAmount  string `json:"original_amount"`  // 交易原币金额 (如 "USD 217.27")
	FormURL         string `json:"form_url"`         // 补充发票的表单链接
	NoteText        string `json:"note_text"`        // 底部署名文本
}

// BuildFormURL constructs a Feishu base form share URL with optional prefill query parameters.
// If enterpriseHost is empty, it falls back to "https://open.feishu.cn".
func BuildFormURL(enterpriseHost, formShareToken, prefillFieldName, recordID string) string {
	raw := strings.TrimSpace(formShareToken)
	if raw == "" {
		return ""
	}
	var base string
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		base = raw
	} else {
		host := strings.TrimRight(strings.TrimSpace(enterpriseHost), "/")
		if host == "" {
			host = "https://open.feishu.cn"
		}
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "https://" + host
		}
		base = fmt.Sprintf("%s/share/base/form/%s", host, raw)
	}

	if prefillFieldName != "" && recordID != "" {
		separator := "?"
		if strings.Contains(base, "?") {
			separator = "&"
		}
		base = fmt.Sprintf("%s%sprefill_%s=%s", base, separator, prefillFieldName, recordID)
	}
	return base
}

// Client delivers message cards using app identity.
type Client struct {
	appID      string
	appSecret  string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a card client for Feishu message dispatching.
func NewClient(appID, appSecret string) *Client {
	return &Client{
		appID:      appID,
		appSecret:  appSecret,
		baseURL:    feishuAPI,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// BuildTransactionCardMap constructs the Feishu interactive card payload object.
func BuildTransactionCardMap(notice TransactionNotice) map[string]any {
	noteText := strings.TrimSpace(notice.NoteText)
	if noteText == "" {
		noteText = "来自 海外易商卡报销助手"
	}

	contentLines := []string{
		"请及时补充海外易商卡消费发票/账单，消费详情如下：",
		fmt.Sprintf("**交易流水号**：%s；", notice.TransactionID),
		fmt.Sprintf("**交易日期**：%s；", notice.TransactionTime),
		fmt.Sprintf("**商户名称**：%s；", notice.Merchant),
		fmt.Sprintf("**交易结算金额**：%s；", notice.BookedAmount),
		fmt.Sprintf("**交易原币金额**：%s", notice.OriginalAmount),
	}

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "turquoise",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "请及时补充海外易商卡消费发票/账单！",
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": strings.Join(contentLines, "\n"),
				},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"type": "primary",
						"text": map[string]any{
							"tag":     "plain_text",
							"content": "补充发票/账单",
						},
						"multi_url": map[string]string{
							"url":         notice.FormURL,
							"pc_url":      notice.FormURL,
							"android_url": notice.FormURL,
							"ios_url":     notice.FormURL,
						},
					},
				},
			},
			map[string]any{
				"tag": "hr",
			},
			map[string]any{
				"tag": "note",
				"elements": []any{
					map[string]any{
						"tag":     "plain_text",
						"content": noteText,
					},
				},
			},
		},
	}
}

// BuildTransactionCardJSON returns the JSON string representation of the card.
func BuildTransactionCardJSON(notice TransactionNotice) (string, error) {
	cardMap := BuildTransactionCardMap(notice)
	bytes, err := json.Marshal(cardMap)
	if err != nil {
		return "", fmt.Errorf("marshal card payload: %w", err)
	}
	return string(bytes), nil
}

// SendCardToUser sends an interactive card to a specific user via open_id.
func (c *Client) SendCardToUser(ctx context.Context, openID string, notice TransactionNotice) (string, error) {
	if openID == "" {
		return "", fmt.Errorf("openID is required")
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch access token: %w", err)
	}

	cardJSON, err := BuildTransactionCardJSON(notice)
	if err != nil {
		return "", err
	}

	payload := map[string]any{
		"receive_id": openID,
		"msg_type":   "interactive",
		"content":    cardJSON,
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal message payload: %w", err)
	}

	endpoint := c.baseURL + "/im/v1/messages?receive_id_type=open_id"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
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

// FetchBotName queries the Feishu bot OpenAPI to retrieve the configured application name.
func (c *Client) FetchBotName(ctx context.Context) (string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch access token: %w", err)
	}

	endpoint := c.baseURL + "/bot/v3/info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create bot info request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch bot info request: %w", err)
	}
	defer resp.Body.Close()

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  struct {
			AppName string `json:"app_name"`
		} `json:"bot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("decode bot info response: %w", err)
	}
	if res.Code != 0 {
		return "", fmt.Errorf("feishu bot info failed (code %d): %s", res.Code, res.Msg)
	}
	return strings.TrimSpace(res.Bot.AppName), nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	reqBody, err := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth/v3/tenant_access_token/internal", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var auth struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&auth); err != nil {
		return "", err
	}
	if auth.Code != 0 || auth.TenantAccessToken == "" {
		return "", fmt.Errorf("Feishu auth failed (code %d): %s", auth.Code, auth.Msg)
	}
	return auth.TenantAccessToken, nil
}
