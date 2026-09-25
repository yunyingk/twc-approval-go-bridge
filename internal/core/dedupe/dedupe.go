// Package dedupe owns the decision whether a change has already been processed.
package dedupe

import "context"

// Claimer atomically reserves a caller-defined change key. A false result means
// the change was already claimed. Key construction and persistence are decided
// when the source event and retry behavior have been verified.
type Claimer interface {
	Claim(ctx context.Context, key string) (claimed bool, err error)
}
