// Package seal contains the outbound submission and inbound callback boundary.
// The wire format and authentication are not known yet, so no route is exposed.
package seal

import "context"

// Submitter sends transformed business data to Seal. The payload format will
// be defined from Seal's actual webhook contract.
type Submitter interface {
	Submit(ctx context.Context, payload []byte) error
}

// CallbackProcessor handles a verified Seal response before Feishu writeback.
// HTTP authentication and payload decoding belong to the future adapter.
type CallbackProcessor interface {
	ProcessCallback(ctx context.Context, payload []byte) error
}
