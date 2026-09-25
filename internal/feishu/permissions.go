package feishu

import (
	"context"
	"encoding/json"
)

// AccessPolicy describes a requested change to record access. The payload is
// intentionally opaque while Feishu's row-level capabilities are unverified.
type AccessPolicy struct {
	Record  RecordRef
	Payload json.RawMessage
}

// AccessController is the boundary for classification and locking operations.
type AccessController interface {
	ApplyAccessPolicy(ctx context.Context, policy AccessPolicy) error
}
