// Package seal contains the outbound submission and inbound callback boundary.
// Outbound webhook transport is implemented by Client; only a local mock
// callback route is exposed until a verified public callback is available.
package seal

import "context"

// Submitter is the original raw-payload boundary. New integrations should use
// Client.SubmitDocument with the confirmed webhook document contract.
type Submitter interface {
	Submit(ctx context.Context, payload []byte) error
}

// CallbackProcessor handles a verified Seal response before Feishu writeback.
// HTTP authentication and payload decoding belong to a future inbound adapter.
type CallbackProcessor interface {
	ProcessCallback(ctx context.Context, payload []byte) error
}
