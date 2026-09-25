package feishu

import (
	"context"
	"encoding/json"
)

// ApprovalGroup is the requested grouping axis. The exact field mapping and
// date representation belong to the future Base-to-business transformer.
type ApprovalGroup struct {
	Date    string
	Project string
	Feature string
}

// ApprovalDraft connects a group of source records to an approval form.
// Form remains opaque until the approval definition is supplied.
type ApprovalDraft struct {
	Group   ApprovalGroup
	Records []RecordRef
	Form    json.RawMessage
}

// ApprovalClient creates an approval and reads its result for later writeback.
type ApprovalClient interface {
	CreateApproval(ctx context.Context, draft ApprovalDraft) (instanceID string, err error)
	GetApproval(ctx context.Context, instanceID string) (json.RawMessage, error)
}
