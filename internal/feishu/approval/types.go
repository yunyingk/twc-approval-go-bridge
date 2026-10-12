package approval

import (
	"time"
)

// DefaultApprovalCode is the production template Code in Feishu tenant for "海外易商卡消费确认单".
const DefaultApprovalCode = "7B19DB2B-1495-4CD8-A723-881F3859EBCE"

// CustomNodeApproval is the custom node ID for the approval node in the template.
const CustomNodeApproval = "0cfd07f250e0d105fa5ed9c12fb5a625"

// CreateInstanceRequest represents the raw JSON request to POST /open-apis/approval/v4/instances.
type CreateInstanceRequest struct {
	ApprovalCode     string          `json:"approval_code"`
	OpenID           string          `json:"open_id,omitempty"`
	UserID           string          `json:"user_id,omitempty"`
	UUID             string          `json:"uuid,omitempty"`
	Form             string          `json:"form"`
	NodeApproverList []NodeApprover  `json:"node_approver_user_id_list,omitempty"`
}

// NodeApprover configures approver open IDs for a specific process node.
type NodeApprover struct {
	Key   string   `json:"key"`
	Value []string `json:"value"`
}

// CreateInstanceResponse represents the response returned by Feishu upon instance creation.
type CreateInstanceResponse struct {
	InstanceCode string `json:"instance_code"`
	InstanceURL  string `json:"instance_url,omitempty"`
}

// ApprovalInput contains all domain facts gathered from Bitable, transactions, invoices, and AI review.
type ApprovalInput struct {
	RecordID         string    `json:"record_id"`
	DocumentSN       string    `json:"document_sn"`
	Title            string    `json:"title"`
	SubmissionDate   time.Time `json:"submission_date"`
	TransactionID    string    `json:"transaction_id"`
	CardholderOpenID string    `json:"cardholder_open_id"`
	ExpenseDate      string    `json:"expense_date"`
	ExpenseCategory  string    `json:"expense_category"`
	ProjectCode      string    `json:"project_code"`
	ExpenseReason    string    `json:"expense_reason"`
	InvoiceKeys      string    `json:"invoice_keys"`
	DemoScenario     string    `json:"demo_scenario"`
	GST              float64   `json:"gst"`
	OriginalAmount   float64   `json:"original_amount"`
	CNYAmount        float64   `json:"cny_amount"`
	Currency         string    `json:"currency"`
	InvoiceAmount    float64   `json:"invoice_amount"`
	ReviewStatus     string    `json:"review_status"`
	ReviewComment    string    `json:"review_comment"`
	AmountDifference string    `json:"amount_difference"`
	RiskLevel        string    `json:"risk_level"`
	Locked           bool      `json:"locked"`
	ParentRecordID   string    `json:"parent_record_id"`

	ApproverOpenID  string `json:"approver_open_id"`
	InitiatorOpenID string `json:"initiator_open_id"`
	UUID            string `json:"uuid"`
}

// FormControl represents an individual form control in the Feishu form JSON array.
type FormControl struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Value   any             `json:"value,omitempty"`
	OpenIDs []string        `json:"open_ids,omitempty"`
}
