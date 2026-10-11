package notify

import (
	"context"
	"time"
)

// DeadLetterNotice carries failure context when a receipt recognition task
// repeatedly fails and is marked as a dead letter to stop retries.
type DeadLetterNotice struct {
	RecordID  string    `json:"record_id"`
	RecordURL string    `json:"record_url,omitempty"`
	FileName  string    `json:"file_name"`
	FileToken string    `json:"file_token"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error"`
	FailedAt  time.Time `json:"failed_at"`
}

// TestNotice carries metadata for channel connectivity diagnostics.
type TestNotice struct {
	TriggeredBy string    `json:"triggered_by,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Note        string    `json:"note,omitempty"`
}

// DeliveryResult records dispatch status for a specific recipient target.
type DeliveryResult struct {
	TargetType string `json:"target_type"` // "chat_id" or "open_id"
	TargetID   string `json:"target_id"`
	MessageID  string `json:"message_id,omitempty"`
	Error      string `json:"error,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
}

// Notifier defines the common interface for broadcasting operational notices.
type Notifier interface {
	NotifyDeadLetter(ctx context.Context, notice DeadLetterNotice) error
	SendTestCard(ctx context.Context, notice TestNotice) ([]DeliveryResult, error)
}
