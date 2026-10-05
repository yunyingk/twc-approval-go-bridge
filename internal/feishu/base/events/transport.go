package events

import transport "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"

// Base filters consume the shared Feishu transport contract. The SDK connection
// and native approval event receiver belong to internal/feishu/events.
type Event = transport.Event
type Sink = transport.Sink
