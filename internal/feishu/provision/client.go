package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultFeishuAPI = "https://open.feishu.cn/open-apis"

// TableFieldReq is the field payload when creating a table or adding a field.
type TableFieldReq struct {
	FieldName string `json:"field_name"`
	Type      int    `json:"type"`
	Property  any    `json:"property,omitempty"`
}

// TableRoleReq defines table permission for a role in Bitable v1.
type TableRoleReq struct {
	TableID           string `json:"table_id"`
	TablePerm         int    `json:"table_perm"` // 0: no_perm, 1: read, 2: edit, 4: admin
	AllowAddRecord    bool   `json:"allow_add_record,omitempty"`
	AllowDeleteRecord bool   `json:"allow_delete_record,omitempty"`
}

// Client interacts with Feishu OpenAPI to provision and administer Bitables.
type Client struct {
	appID      string
	appSecret  string
	apiBase    string
	httpClient *http.Client
}

// NewClient returns a provision Client initialized with Feishu credentials.
func NewClient(appID, appSecret string) *Client {
	return &Client{
		appID:      appID,
		appSecret:  appSecret,
		apiBase:    defaultFeishuAPI,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SetAPIBase allows overriding openapi URL in tests.
func (c *Client) SetAPIBase(base string) {
	c.apiBase = strings.TrimRight(base, "/")
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	var auth struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	body := map[string]string{"app_id": c.appID, "app_secret": c.appSecret}
	if err := c.request(ctx, http.MethodPost, c.apiBase+"/auth/v3/tenant_access_token/internal", "", body, &auth); err != nil {
		return "", err
	}
	if auth.Code != 0 || auth.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu authentication failed (code %d): %s", auth.Code, auth.Msg)
	}
	return auth.TenantAccessToken, nil
}

func (c *Client) request(ctx context.Context, method, endpoint, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			RawBody:    string(respBytes),
		}
	}

	if out != nil {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("unmarshal response (%s): %w", string(respBytes), err)
		}
	}
	return nil
}

// APIError wraps a non-2xx Feishu HTTP response.
type APIError struct {
	StatusCode int
	Code       int
	Msg        string
	RawBody    string
}

func (e *APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("feishu API error (http %d, code %d): %s", e.StatusCode, e.Code, e.Msg)
	}
	return fmt.Sprintf("feishu API error (http %d): %s", e.StatusCode, e.RawBody)
}

// BaseAppInfo holds created Base metadata.
type BaseAppInfo struct {
	AppToken       string `json:"app_token"`
	DefaultTableID string `json:"default_table_id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
}

// CreateBase creates a new Bitable instance in the app's root space or specified folder.
func (c *Client) CreateBase(ctx context.Context, name, folderToken string) (*BaseAppInfo, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	body := map[string]string{"name": name}
	if folderToken != "" {
		body["folder_token"] = folderToken
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			App BaseAppInfo `json:"app"`
		} `json:"data"`
	}

	endpoint := c.apiBase + "/bitable/v1/apps"
	if err := c.request(ctx, http.MethodPost, endpoint, token, body, &res); err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return &res.Data.App, nil
}

// CreatedTableInfo holds metadata for a table created with fields.
type CreatedTableInfo struct {
	TableID     string   `json:"table_id"`
	FieldIDList []string `json:"field_id_list"`
}

// CreateTable creates a data table with initial fields in the Base.
func (c *Client) CreateTable(ctx context.Context, baseToken, name string, fields []TableFieldReq) (*CreatedTableInfo, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"table": map[string]any{
			"name":              name,
			"default_view_name": "全部",
			"fields":            fields,
		},
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			TableID     string   `json:"table_id"`
			FieldIDList []string `json:"field_id_list"`
		} `json:"data"`
	}

	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables", c.apiBase, url.PathEscape(baseToken))
	if err := c.request(ctx, http.MethodPost, endpoint, token, body, &res); err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return &CreatedTableInfo{
		TableID:     res.Data.TableID,
		FieldIDList: res.Data.FieldIDList,
	}, nil
}

// DeleteTable deletes a table from the Base (useful for removing the initial empty default table).
func (c *Client) DeleteTable(ctx context.Context, baseToken, tableID string) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}

	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s", c.apiBase, url.PathEscape(baseToken), url.PathEscape(tableID))
	if err := c.request(ctx, http.MethodDelete, endpoint, token, nil, &res); err != nil {
		return err
	}
	if res.Code != 0 {
		return &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return nil
}

// CreateField adds a field (such as a link field) to an existing table.
func (c *Client) CreateField(ctx context.Context, baseToken, tableID, name string, fieldType int, property any) (string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}

	body := TableFieldReq{
		FieldName: name,
		Type:      fieldType,
		Property:  property,
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Field struct {
				FieldID   string `json:"field_id"`
				FieldName string `json:"field_name"`
			} `json:"field"`
		} `json:"data"`
	}

	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/fields", c.apiBase, url.PathEscape(baseToken), url.PathEscape(tableID))
	if err := c.request(ctx, http.MethodPost, endpoint, token, body, &res); err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return res.Data.Field.FieldID, nil
}

// EnableAdvPerm enables advanced permissions on the Base.
func (c *Client) EnableAdvPerm(ctx context.Context, baseToken string) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}

	endpoint := fmt.Sprintf("%s/base/v3/bases/%s/advperm/enable?enable=true", c.apiBase, url.PathEscape(baseToken))
	if err := c.request(ctx, http.MethodPut, endpoint, token, nil, &res); err != nil {
		return err
	}
	if res.Code != 0 {
		return &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return nil
}

// CreateRole creates a custom role with table-level permissions in Bitable v1.
func (c *Client) CreateRole(ctx context.Context, baseToken, roleName string, tableRoles []TableRoleReq) (string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}

	body := map[string]any{
		"role_name":   roleName,
		"table_roles": tableRoles,
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Role struct {
				RoleID   string `json:"role_id"`
				RoleName string `json:"role_name"`
			} `json:"role"`
		} `json:"data"`
	}

	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/roles", c.apiBase, url.PathEscape(baseToken))
	if err := c.request(ctx, http.MethodPost, endpoint, token, body, &res); err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return res.Data.Role.RoleID, nil
}

// AddCollaborator adds a member (e.g. finance user) as a collaborator (e.g. "full_access" or "edit").
func (c *Client) AddCollaborator(ctx context.Context, baseToken, memberType, memberID, perm string) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	body := map[string]string{
		"member_type": memberType,
		"member_id":   memberID,
		"perm":        perm,
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}

	endpoint := fmt.Sprintf("%s/drive/v1/permissions/%s/members?type=bitable", c.apiBase, url.PathEscape(baseToken))
	if err := c.request(ctx, http.MethodPost, endpoint, token, body, &res); err != nil {
		return err
	}
	if res.Code != 0 {
		return &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return nil
}

// TransferOwner transfers ownership of the Bitable to a specified user.
func (c *Client) TransferOwner(ctx context.Context, baseToken, memberType, memberID string) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	body := map[string]string{
		"member_type": memberType,
		"member_id":   memberID,
	}

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}

	endpoint := fmt.Sprintf("%s/drive/v1/permissions/%s/members/transfer_owner?type=bitable", c.apiBase, url.PathEscape(baseToken))
	if err := c.request(ctx, http.MethodPost, endpoint, token, body, &res); err != nil {
		return err
	}
	if res.Code != 0 {
		return &APIError{StatusCode: 200, Code: res.Code, Msg: res.Msg}
	}
	return nil
}
