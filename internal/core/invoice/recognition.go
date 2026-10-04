// Package invoice defines the provider-independent invoice recognition boundary.
package invoice

import (
	"context"
	"encoding/json"
)

// Attachment is the input shared by the Anyreceipt and future AI adapters.
type Attachment struct {
	Name        string
	ContentType string
	URL         string
	Data        []byte
}

// Recognition retains provider fields and the original response.
// Mapping these fields into Bitable remains a separate step.
type Recognition struct {
	Title   string
	Country string
	Type    string
	Total   string
	Tax     string
	Date    string
	Number  string
	Summary string
	Outputs map[string]json.RawMessage
	Raw     json.RawMessage
	// Facts are the provider-independent values consumed by downstream adapters.
	// Outputs remains available for legacy records and original provider evidence.
	Facts  *Facts `json:"facts,omitempty"`
	Origin Origin `json:"origin,omitempty"`
}

type Facts struct {
	Title            string `json:"title,omitempty"`
	Country          string `json:"country,omitempty"`
	ReceiptType      string `json:"receipt_type,omitempty"`
	Number           string `json:"number,omitempty"`
	IssueDate        string `json:"issue_date,omitempty"`
	Currency         string `json:"currency,omitempty"`
	Seller           string `json:"seller,omitempty"`
	Buyer            string `json:"buyer,omitempty"`
	BusinessCategory string `json:"business_category,omitempty"`
	TaxRate          string `json:"tax_rate,omitempty"`
	Total            string `json:"total,omitempty"`
	Tax              string `json:"tax,omitempty"`
	Pretax           string `json:"pretax,omitempty"`
}

type Origin struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	TraceID  string `json:"trace_id,omitempty"`
}

// Recognizer allows the receipt provider to be replaced without changing the
// Feishu or Seal domains.
type Recognizer interface {
	Recognize(ctx context.Context, attachment Attachment) (Recognition, error)
}
