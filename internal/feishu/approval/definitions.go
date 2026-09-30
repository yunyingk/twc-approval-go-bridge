package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
)

// DefinitionClient manages native approval templates with an explicit app identity.
// It is independent of Base, receipt processing and approval instances.
type DefinitionClient struct {
	sdk *larkapproval.V4
}

// DefinitionRef contains the identifiers returned by Feishu after creation.
type DefinitionRef struct {
	Code string `json:"approval_code"`
	ID   string `json:"approval_id"`
}

// NewDefinitionClient does not make network requests. The caller chooses the
// tenant by supplying that tenant's app credentials; there is no identity fallback.
func NewDefinitionClient(appID, appSecret string, httpClient *http.Client) (*DefinitionClient, error) {
	appID, appSecret = strings.TrimSpace(appID), strings.TrimSpace(appSecret)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("Feishu approval app ID and app secret are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	config := &larkcore.Config{
		BaseUrl:          "https://open.feishu.cn",
		AppId:            appID,
		AppSecret:        appSecret,
		AppType:          larkcore.AppTypeSelfBuilt,
		EnableTokenCache: true,
		ReqTimeout:       30 * time.Second,
		HttpClient:       httpClient,
		LogLevel:         larkcore.LogLevelError,
	}
	larkcore.NewLogger(config)
	larkcore.NewCache(config)
	larkcore.NewSerialization(config)
	larkcore.NewHttpClient(config)
	return &DefinitionClient{sdk: larkapproval.New(config)}, nil
}

// ValidateNewDefinition checks the creation boundary, leaving widget and process
// semantics to the official API. Supplying approval_code would replace a template.
func ValidateNewDefinition(definition *larkapproval.ApprovalCreate) error {
	if definition == nil {
		return fmt.Errorf("approval definition is required")
	}
	if definition.ApprovalCode != nil {
		return fmt.Errorf("approval_code must be omitted when creating a new template")
	}
	if definition.ApprovalName == nil || strings.TrimSpace(*definition.ApprovalName) == "" ||
		len(definition.Viewers) == 0 || len(definition.NodeList) < 2 || len(definition.I18nResources) == 0 {
		return fmt.Errorf("approval_name, viewers, node_list and i18n_resources are required")
	}
	if definition.Form == nil || definition.Form.FormContent == nil {
		return fmt.Errorf("form.form_content is required")
	}
	var widgets []json.RawMessage
	if err := json.Unmarshal([]byte(*definition.Form.FormContent), &widgets); err != nil || len(widgets) == 0 {
		return fmt.Errorf("form.form_content must be a JSON string containing a nonempty widget array")
	}
	return nil
}

// CreateDefinition creates one template. This method has no application-level
// retry: a lost response must be investigated before creating another template.
// API-created templates cannot be disabled or deleted according to Feishu docs.
func (c *DefinitionClient) CreateDefinition(ctx context.Context, definition *larkapproval.ApprovalCreate) (DefinitionRef, error) {
	if c == nil || c.sdk == nil {
		return DefinitionRef{}, fmt.Errorf("Feishu approval client is not initialized")
	}
	if err := ValidateNewDefinition(definition); err != nil {
		return DefinitionRef{}, err
	}
	req := larkapproval.NewCreateApprovalReqBuilder().
		UserIdType("open_id").DepartmentIdType("open_department_id").
		ApprovalCreate(definition).Build()
	resp, err := c.sdk.Approval.Create(ctx, req)
	if err != nil {
		return DefinitionRef{}, fmt.Errorf("create Feishu approval definition: %w", err)
	}
	if !resp.Success() {
		return DefinitionRef{}, fmt.Errorf("create Feishu approval definition: code %d: %s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.ApprovalCode == nil || *resp.Data.ApprovalCode == "" ||
		resp.Data.ApprovalId == nil || *resp.Data.ApprovalId == "" {
		return DefinitionRef{}, fmt.Errorf("Feishu creation response lacks template identifiers; check the tenant before retrying")
	}
	return DefinitionRef{Code: *resp.Data.ApprovalCode, ID: *resp.Data.ApprovalId}, nil
}

// GetDefinition reads the template using the same app identity as creation.
// Feishu returns Form as a JSON string; it remains in the official response type.
func (c *DefinitionClient) GetDefinition(ctx context.Context, code string) (*larkapproval.GetApprovalRespData, error) {
	if c == nil || c.sdk == nil {
		return nil, fmt.Errorf("Feishu approval client is not initialized")
	}
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("approval code is required")
	}
	req := larkapproval.NewGetApprovalReqBuilder().ApprovalCode(code).
		Locale("zh-CN").UserIdType("open_id").WithOption(true).Build()
	resp, err := c.sdk.Approval.Get(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get Feishu approval definition: %w", err)
	}
	if !resp.Success() {
		return nil, fmt.Errorf("get Feishu approval definition: code %d: %s", resp.Code, resp.Msg)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("Feishu definition response lacks data")
	}
	return resp.Data, nil
}
