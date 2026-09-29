// Package receipt defines the provider-independent receipt recognition boundary.
package receipt

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

// Recognition uses only fields already returned by the existing Anyreceipt
// field shortcut. Mapping these fields into Bitable remains a separate step.
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
}

// Recognizer allows the receipt provider to be replaced without changing the
// Feishu or Seal domains.
type Recognizer interface {
	Recognize(ctx context.Context, attachment Attachment) (Recognition, error)
}
