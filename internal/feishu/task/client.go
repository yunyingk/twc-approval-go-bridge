// Package task provides a client for Feishu Task v2 API.
package task

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

// CreateTaskParam holds options for creating a Feishu task.
type CreateTaskParam struct {
	Summary        string
	Description    string
	AssigneeOpenID string
	StartTime      *time.Time
	DueTime        *time.Time
	OriginTitle    string
	OriginURL      string
	PlatformName   string
}

// Client delivers Task v2 API requests using tenant access tokens.
type Client struct {
	appID      string
	appSecret  string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a task client.
func NewClient(appID, appSecret string) *Client {
	return &Client{
		appID:      appID,
		appSecret:  appSecret,
		baseURL:    feishuAPI,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetBaseURLForTest overrides the API base URL in unit tests.
func (c *Client) SetBaseURLForTest(u string) {
	c.baseURL = u
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

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 || result.TenantAccessToken == "" {
		return "", fmt.Errorf("fetch access token failed (code %d): %s", result.Code, result.Msg)
	}
	return result.TenantAccessToken, nil
}

// CreateTask creates a new Feishu task v2 with optional start, due, and origin link.
func (c *Client) CreateTask(ctx context.Context, param CreateTaskParam) (string, error) {
	if strings.TrimSpace(param.Summary) == "" {
		return "", fmt.Errorf("task summary is required")
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}

	payload := map[string]any{
		"summary": param.Summary,
	}

	if param.Description != "" {
		payload["description"] = param.Description
	}

	if param.AssigneeOpenID != "" {
		payload["members"] = []map[string]any{
			{
				"id":   param.AssigneeOpenID,
				"role": "assignee",
				"type": "user",
			},
		}
	}

	if param.StartTime != nil {
		payload["start"] = map[string]any{
			"timestamp":  fmt.Sprintf("%d", param.StartTime.UnixMilli()),
			"is_all_day": true,
		}
	}

	if param.DueTime != nil {
		payload["due"] = map[string]any{
			"timestamp":  fmt.Sprintf("%d", param.DueTime.UnixMilli()),
			"is_all_day": true,
		}
	}

	if param.OriginURL != "" {
		platform := param.PlatformName
		if platform == "" {
			platform = "海外易商卡报销助手"
		}
		title := param.OriginTitle
		if title == "" {
			title = "点击直达单据"
		}
		payload["origin"] = map[string]any{
			"platform_i18n_name": map[string]string{
				"zh_cn": platform,
				"en_us": platform,
			},
			"href": map[string]string{
				"title": title,
				"url":   param.OriginURL,
			},
		}
	}

	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := c.baseURL + "/task/v2/tasks?user_id_type=open_id"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Task struct {
				GUID string `json:"guid"`
			} `json:"task"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", fmt.Errorf("unmarshal create task response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("create task failed (code %d): %s", result.Code, result.Msg)
	}

	return result.Data.Task.GUID, nil
}

// UpdateTask updates specific fields of a Feishu task.
func (c *Client) UpdateTask(ctx context.Context, taskGUID string, fields map[string]any, updateFields []string) error {
	if strings.TrimSpace(taskGUID) == "" {
		return fmt.Errorf("taskGUID is required")
	}
	if len(updateFields) == 0 {
		return nil
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"task":          fields,
		"update_fields": updateFields,
	}
	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/task/v2/tasks/%s", c.baseURL, taskGUID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(reqBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return fmt.Errorf("unmarshal update task response: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("update task failed (code %d): %s", result.Code, result.Msg)
	}
	return nil
}

// CompleteTask marks a task as completed.
func (c *Client) CompleteTask(ctx context.Context, taskGUID string) error {
	nowMilli := fmt.Sprintf("%d", time.Now().UnixMilli())
	return c.UpdateTask(ctx, taskGUID, map[string]any{
		"completed_at": nowMilli,
	}, []string{"completed_at"})
}

// CreateComment posts a comment to a specific task.
func (c *Client) CreateComment(ctx context.Context, taskGUID, content string) error {
	if strings.TrimSpace(taskGUID) == "" {
		return fmt.Errorf("taskGUID is required")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("comment content is required")
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"content":       content,
		"resource_type": "task",
		"resource_id":   taskGUID,
	}
	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := c.baseURL + "/task/v2/comments"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return fmt.Errorf("unmarshal create comment response: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("create comment failed (code %d): %s", result.Code, result.Msg)
	}
	return nil
}
