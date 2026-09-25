// Package receipt defines the provider-independent receipt recognition boundary.
package receipt

import "context"

// Attachment is the input shared by the Anyreceipt and future AI adapters.
type Attachment struct {
	Name        string
	ContentType string
	URL         string
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
}

// Recognizer allows the receipt provider to be replaced without changing the
// Feishu or Seal domains.
type Recognizer interface {
	Recognize(ctx context.Context, attachment Attachment) (Recognition, error)
}
