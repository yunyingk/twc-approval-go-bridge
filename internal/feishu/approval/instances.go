package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
)

// ErrUUIDConflict is not a rejected creation: that UUID may already identify an
// accepted instance. Query the same UUID before deciding what happened.
var ErrUUIDConflict = errors.New("approval UUID already exists")

type InstanceClient struct {
	definitions *DefinitionClient
	scope       string
}

// NewInstanceClient uses one explicit application identity for definitions,
// creation and UUID lookup. Construction never sends a request.
func NewInstanceClient(appID, appSecret string, httpClient *http.Client) (*InstanceClient, error) {
	definitions, err := NewDefinitionClient(appID, appSecret, httpClient)
	if err != nil {
		return nil, err
	}
	return &InstanceClient{definitions: definitions, scope: "feishu-app:" + strings.TrimSpace(appID)}, nil
}

func (c *InstanceClient) TargetScope() string { return c.scope }

type InstanceRequest struct {
	ApprovalCode string          `json:"approval_code"`
	OpenID       string          `json:"open_id"`
	DepartmentID string          `json:"department_id,omitempty"`
	UUID         string          `json:"uuid"`
	Form         json.RawMessage `json:"form"`
	// These open IDs must belong to the same application as OpenID.
	NodeApprovers map[string][]string `json:"node_approvers,omitempty"`
}

type InstanceRef struct {
	Code string `json:"instance_code"`
	Link string `json:"instance_link,omitempty"`
}

// InstanceAPIError deliberately excludes upstream messages and response bodies.
type InstanceAPIError struct {
	Operation  string
	StatusCode int
	Code       int
}

func (e *InstanceAPIError) Error() string {
	return fmt.Sprintf("Feishu approval %s: HTTP %d code %d", e.Operation, e.StatusCode, e.Code)
}
func (e *InstanceAPIError) HTTPStatus() int         { return e.StatusCode }
func (e *InstanceAPIError) RemoteErrorCode() string { return strconv.Itoa(e.Code) }

// A transport/protocol error leaves creation uncertain. Its safe Error string
// avoids SDK errors that may include a full upstream response or form values.
type instanceTransportError struct {
	operation string
	cause     error
}

func (e *instanceTransportError) Error() string {
	return "Feishu approval " + e.operation + ": transport or response protocol failure"
}
func (e *instanceTransportError) Unwrap() error { return e.cause }

func validateInstanceResponse(resp *larkcore.ApiResp) error {
	var envelope struct {
		Code *int `json:"code"`
	}
	if resp == nil || json.Unmarshal(resp.RawBody, &envelope) != nil || envelope.Code == nil ||
		(*envelope.Code == 0 && (resp.StatusCode < 200 || resp.StatusCode >= 300)) {
		return fmt.Errorf("approval response lacks an explicit successful protocol envelope")
	}
	return nil
}

func (r InstanceRequest) Validate() error {
	for field, value := range map[string]string{"approval_code": r.ApprovalCode, "uuid": r.UUID, "open_id": r.OpenID} {
		if value == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("approval %s is required without surrounding whitespace", field)
		}
	}
	if len(r.UUID) > 64 || !validOpenID(r.OpenID) || r.DepartmentID != strings.TrimSpace(r.DepartmentID) {
		return fmt.Errorf("approval UUID, submitter open ID or department ID is invalid")
	}
	if err := validateInstanceForm(r.Form); err != nil {
		return err
	}
	if len(r.NodeApprovers) > 20 {
		return fmt.Errorf("approval supports at most 20 selected approver nodes")
	}
	for node, ids := range r.NodeApprovers {
		if node == "" || node != strings.TrimSpace(node) || len(ids) == 0 {
			return fmt.Errorf("approval node approvers require a node and open IDs")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !validOpenID(id) || seen[id] {
				return fmt.Errorf("approval node approver open IDs are invalid or duplicated")
			}
			seen[id] = true
		}
	}
	return nil
}

func validOpenID(id string) bool {
	return strings.HasPrefix(id, "ou_") && len(id) > 3 && id == strings.TrimSpace(id)
}

func (c *InstanceClient) GetDefinition(ctx context.Context, code string) (*larkapproval.GetApprovalRespData, error) {
	if c == nil || c.definitions == nil {
		return nil, fmt.Errorf("Feishu instance client is not initialized")
	}
	return c.definitions.GetDefinition(ctx, code)
}

// CreateInstance sends exactly one creation request. The caller must persist
// the UUID and source-version mapping first; this method never generates a UUID
// or retries a lost response. The form is an array locally and a string on wire.
func (c *InstanceClient) CreateInstance(ctx context.Context, draft InstanceRequest) (InstanceRef, error) {
	if c == nil || c.definitions == nil {
		return InstanceRef{}, fmt.Errorf("Feishu instance client is not initialized")
	}
	if err := draft.Validate(); err != nil {
		return InstanceRef{}, err
	}
	body := larkapproval.NewInstanceCreateBuilder().ApprovalCode(draft.ApprovalCode).
		OpenId(draft.OpenID).Uuid(draft.UUID).Form(string(draft.Form)).
		AllowResubmit(false).AllowSubmitAgain(false)
	if draft.DepartmentID != "" {
		body.DepartmentId(draft.DepartmentID)
	}
	nodes := make([]*larkapproval.NodeApprover, 0, len(draft.NodeApprovers))
	keys := make([]string, 0, len(draft.NodeApprovers))
	for node := range draft.NodeApprovers {
		keys = append(keys, node)
	}
	sort.Strings(keys)
	for _, node := range keys {
		nodes = append(nodes, larkapproval.NewNodeApproverBuilder().Key(node).Value(draft.NodeApprovers[node]).Build())
	}
	if len(nodes) > 0 {
		body.NodeApproverOpenIdList(nodes)
	}
	resp, err := c.definitions.sdk.Instance.Create(ctx, larkapproval.NewCreateInstanceReqBuilder().InstanceCreate(body.Build()).Build())
	if err != nil {
		return InstanceRef{}, &instanceTransportError{"create", err}
	}
	if err := validateInstanceResponse(resp.ApiResp); err != nil {
		return InstanceRef{}, &instanceTransportError{"create", err}
	}
	if !resp.Success() {
		failure := &InstanceAPIError{"create", resp.StatusCode, resp.Code}
		if resp.Code == 60012 {
			return InstanceRef{}, errors.Join(ErrUUIDConflict, failure)
		}
		return InstanceRef{}, failure
	}
	if resp.Data == nil || resp.Data.InstanceCode == nil || strings.TrimSpace(*resp.Data.InstanceCode) == "" {
		return InstanceRef{}, &instanceTransportError{operation: "create"}
	}
	ref := InstanceRef{Code: *resp.Data.InstanceCode}
	if resp.Data.InstanceLink != nil {
		ref.Link = *resp.Data.InstanceLink
	}
	return ref, nil
}

// GetInstance accepts either a native instance code or the original creation
// UUID, as documented by Feishu. It never follows user OAuth or reads Base.
func (c *InstanceClient) GetInstance(ctx context.Context, codeOrUUID string) (*larkapproval.GetInstanceRespData, error) {
	if c == nil || c.definitions == nil || strings.TrimSpace(codeOrUUID) == "" {
		return nil, fmt.Errorf("initialized instance client and instance code or UUID are required")
	}
	req := larkapproval.NewGetInstanceReqBuilder().InstanceId(codeOrUUID).Locale("zh-CN").UserIdType("open_id").Build()
	resp, err := c.definitions.sdk.Instance.Get(ctx, req)
	if err != nil {
		return nil, &instanceTransportError{"get", err}
	}
	if err := validateInstanceResponse(resp.ApiResp); err != nil {
		return nil, &instanceTransportError{"get", err}
	}
	if !resp.Success() {
		return nil, &InstanceAPIError{"get", resp.StatusCode, resp.Code}
	}
	if resp.Data == nil || resp.Data.InstanceCode == nil || *resp.Data.InstanceCode == "" || resp.Data.Status == nil {
		return nil, &instanceTransportError{operation: "get"}
	}
	return resp.Data, nil
}
