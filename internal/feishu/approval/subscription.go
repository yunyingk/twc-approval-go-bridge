package approval

import (
	"context"
	"fmt"
	"strings"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
)

// SubscribeApprovalEvents performs one explicit template subscription. The
// caller must also configure the event and permissions in the developer console.
// Code 1390007 covers both subscribed and canceled, so it is not success here.
func (c *InstanceClient) SubscribeApprovalEvents(ctx context.Context, code string) error {
	if c == nil || c.definitions == nil || code == "" || code != strings.TrimSpace(code) {
		return fmt.Errorf("initialized approval client and explicit template code are required")
	}
	resp, err := c.definitions.sdk.Approval.Subscribe(ctx, larkapproval.NewSubscribeApprovalReqBuilder().ApprovalCode(code).Build())
	if err != nil {
		return &instanceTransportError{"subscribe", err}
	}
	if err := validateInstanceResponse(resp.ApiResp); err != nil {
		return &instanceTransportError{"subscribe", err}
	}
	if !resp.Success() {
		return &InstanceAPIError{"subscribe", resp.StatusCode, resp.Code}
	}
	return nil
}
