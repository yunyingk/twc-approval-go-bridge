// Package feishu defines the service-facing operations on Feishu resources.
// The transport implementations can be added after the target Base is mapped.
package feishu

import (
	"context"
	"encoding/json"
)

// RecordRef identifies one Bitable record without assuming a field schema.
type RecordRef struct {
	BaseToken string
	TableID   string
	RecordID  string
}

// Record keeps field values opaque until the business mapping is confirmed.
type Record struct {
	Ref    RecordRef
	Fields map[string]json.RawMessage
}

// RecordReader reads a Bitable record for the internal transformation step.
type RecordReader interface {
	ReadRecord(ctx context.Context, ref RecordRef) (Record, error)
}

// RecordWriter writes selected results back to the originating record.
type RecordWriter interface {
	PatchRecord(ctx context.Context, ref RecordRef, fields map[string]json.RawMessage) error
}
