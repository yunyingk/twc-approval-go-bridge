package seal

import (
	"encoding/json"
	"fmt"
	"strings"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// DecodeCallback converts an already authenticated provider payload. It does not
// invent a public authentication scheme; transports verify authenticity first.
func DecodeCallback(raw []byte) (string, core.Outcome, error) {
	var payload struct {
		DocumentID          string `json:"documentId"`
		DocumentIDAlt       string `json:"document_id"`
		ApprovalRecordID    string `json:"approvalRecordId"`
		ApprovalRecordIDAlt string `json:"approval_record_id"`
		Decision            string `json:"decision"`
		Comment             string `json:"comment"`
		ApprovalURL         string `json:"approvalUrl"`
		ApprovalURLAlt      string `json:"approval_url"`
	}
	if len(raw) > maxResponseBytes {
		return "", core.Outcome{}, fmt.Errorf("Seal callback exceeds size limit")
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", core.Outcome{}, fmt.Errorf("invalid Seal callback json: %w", err)
	}
	if payload.DocumentID == "" && payload.DocumentIDAlt != "" {
		payload.DocumentID = payload.DocumentIDAlt
	}
	if payload.ApprovalRecordID == "" && payload.ApprovalRecordIDAlt != "" {
		payload.ApprovalRecordID = payload.ApprovalRecordIDAlt
	}
	if payload.ApprovalURL == "" && payload.ApprovalURLAlt != "" {
		payload.ApprovalURL = payload.ApprovalURLAlt
	}
	if payload.DocumentID == "" || payload.ApprovalRecordID == "" {
		return "", core.Outcome{}, fmt.Errorf("invalid Seal callback identity")
	}
	decision := strings.ToLower(strings.TrimSpace(payload.Decision))
	outcome := core.Outcome{
		Decision:   decision,
		Comment:    payload.Comment,
		ExternalID: payload.ApprovalRecordID,
		URL:        payload.ApprovalURL,
	}
	return payload.DocumentID, outcome, outcome.Validate()
}
