// Package feishuws contains the optional Feishu persistent-connection adapter.
package feishuws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

const DefaultEventType = "drive.file.bitable_record_changed_v1"

// Event is the transport-level representation passed to the future application layer.
// Payload remains untouched so business decoding can be added after the event contract
// has been verified against a real tenant.
type Event struct {
	ID      string
	Type    string
	Payload json.RawMessage
}

// Sink receives events delivered by the SDK.
type Sink func(context.Context, Event) error

// Listener wraps the SDK WebSocket client and keeps SDK details out of main.
type Listener struct {
	client *larkws.Client
}

// New creates a persistent-connection listener without opening a network connection.
func New(appID, appSecret, eventType string, sink Sink, logger *slog.Logger) (*Listener, error) {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	eventType = strings.TrimSpace(eventType)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("Feishu app ID and app secret are required")
	}
	if eventType == "" {
		eventType = DefaultEventType
	}
	if logger == nil {
		logger = slog.Default()
	}

	handler := dispatcher.NewEventDispatcher("", "").OnCustomizedEvent(eventType,
		func(ctx context.Context, req *larkevent.EventReq) error {
			event := decodeEvent(req.Body, eventType)
			if sink == nil {
				return nil
			}
			return sink(ctx, event)
		},
	)

	client := larkws.NewClient(
		appID,
		appSecret,
		larkws.WithEventHandler(handler),
		larkws.WithOnReady(func() {
			logger.Info("Feishu long connection ready", "event_type", eventType)
		}),
		larkws.WithOnReconnecting(func() {
			logger.Warn("Feishu long connection reconnecting")
		}),
		larkws.WithOnReconnected(func() {
			logger.Info("Feishu long connection reconnected")
		}),
		larkws.WithOnDisconnected(func() {
			logger.Warn("Feishu long connection disconnected")
		}),
		larkws.WithOnError(func(err error) {
			logger.Error("Feishu long connection error", "error", err)
		}),
	)

	return &Listener{client: client}, nil
}

// Start blocks while the SDK maintains the connection.
func (l *Listener) Start(ctx context.Context) error {
	if l == nil || l.client == nil {
		return fmt.Errorf("Feishu listener is not initialized")
	}
	return l.client.Start(ctx)
}

// CloseAndWait stops the SDK client and waits for its workers to finish.
func (l *Listener) CloseAndWait(ctx context.Context) error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.CloseAndWait(ctx)
}

type eventEnvelope struct {
	Header struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
	} `json:"header"`
}

func decodeEvent(payload []byte, fallbackType string) Event {
	event := Event{
		Type:    fallbackType,
		Payload: append(json.RawMessage(nil), payload...),
	}
	var envelope eventEnvelope
	if err := json.Unmarshal(payload, &envelope); err == nil {
		event.ID = envelope.Header.EventID
		if envelope.Header.EventType != "" {
			event.Type = envelope.Header.EventType
		}
	}
	return event
}

// LoggingSink returns a transport-only sink suitable for the initial service.
// Raw payload logging is opt-in because event bodies may contain business data.
func LoggingSink(logger *slog.Logger, logRaw bool) Sink {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, event Event) error {
		attrs := []any{
			"event_id", event.ID,
			"event_type", event.Type,
			"payload_bytes", len(event.Payload),
		}
		if logRaw {
			attrs = append(attrs, "payload", string(event.Payload))
		}
		logger.InfoContext(ctx, "Feishu event received", attrs...)
		return nil
	}
}
