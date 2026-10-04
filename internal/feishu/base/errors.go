package base

import (
	"fmt"
	"strconv"
)

// APIError preserves the platform code so callers never infer deletion from
// a permission failure or from human-readable error text.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) RemoteErrorCode() string { return strconv.Itoa(e.Code) }

func (e *APIError) Error() string {
	return fmt.Sprintf("Feishu API code %d: %s", e.Code, e.Message)
}
