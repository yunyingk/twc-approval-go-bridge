package seal

import (
	"encoding/json"
	"fmt"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// DecodeCallback converts an already authenticated provider payload. It does not
// invent a public authentication scheme; transports verify authenticity first.
func DecodeCallback(raw []byte) (string, core.Outcome, error) {
	var payload struct {
		DocumentID       string `json:"documentId"`
		ApprovalRecordID string `json:"approvalRecordId"`
		Decision         string `json:"decision"`
		Comment          string `json:"comment"`
		ApprovalURL      string `json:"approvalUrl"`
	}
	if len(raw) > maxResponseBytes {
		return "", core.Outcome{}, fmt.Errorf("Seal callback exceeds size limit")
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.DocumentID == "" || payload.ApprovalRecordID == "" {
		return "", core.Outcome{}, fmt.Errorf("invalid Seal callback identity")
	}
	outcome := core.Outcome{Decision: payload.Decision, Comment: payload.Comment, ExternalID: payload.ApprovalRecordID, URL: payload.ApprovalURL}
	return payload.DocumentID, outcome, outcome.Validate()
}
