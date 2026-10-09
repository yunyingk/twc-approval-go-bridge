// Package events contains the optional Feishu Bitable persistent-connection adapter.
// The connection is now shared with native approval events; Base-specific
// attachment and review filters remain in internal/feishu/base/events.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

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

// ConnectionStatus records the live state of the persistent WebSocket connection.
type ConnectionStatus struct {
	mu          sync.RWMutex
	State       string    `json:"state"` // "ready", "reconnecting", "disconnected", "disabled"
	ConnectedAt time.Time `json:"connected_at,omitempty"`
	LastEventAt time.Time `json:"last_event_at,omitempty"`
	EventCount  int64     `json:"event_count"`
	LastError   string    `json:"last_error,omitempty"`
}

func (s *ConnectionStatus) Snapshot() (state string, connectedAt, lastEventAt time.Time, count int64) {
	if s == nil {
		return "disabled", time.Time{}, time.Time{}, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State, s.ConnectedAt, s.LastEventAt, s.EventCount
}

// Listener wraps the SDK WebSocket client and keeps SDK details out of main.
type Listener struct {
	client *larkws.Client
	status *ConnectionStatus
}

// Status returns current connection metrics for diagnostics and health dashboards.
func (l *Listener) Status() (state string, connectedAt, lastEventAt time.Time, count int64) {
	if l == nil || l.status == nil {
		return "disabled", time.Time{}, time.Time{}, 0
	}
	return l.status.Snapshot()
}

// New creates a persistent-connection listener without opening a network connection.
func New(appID, appSecret, eventType string, sink Sink, logger *slog.Logger) (*Listener, error) {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		eventType = DefaultEventType
	}
	return NewForEvents(appID, appSecret, map[string]Sink{eventType: sink}, logger)
}

// NewForEvents shares one authenticated app connection between explicitly
// registered event types. A separate approval app gets its own connection.
func NewForEvents(appID, appSecret string, sinks map[string]Sink, logger *slog.Logger) (*Listener, error) {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("Feishu app ID and app secret are required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	status := &ConnectionStatus{State: "connecting"}
	wrappedSinks := make(map[string]Sink, len(sinks))
	for eventType, sink := range sinks {
		wrappedSinks[eventType] = func(ctx context.Context, ev Event) error {
			status.mu.Lock()
			status.LastEventAt = time.Now()
			status.EventCount++
			status.mu.Unlock()
			if sink != nil {
				return sink(ctx, ev)
			}
			return nil
		}
	}

	handler, types, err := newEventDispatcher(wrappedSinks)
	if err != nil {
		return nil, err
	}

	client := larkws.NewClient(
		appID,
		appSecret,
		larkws.WithEventHandler(handler),
		larkws.WithOnReady(func() {
			status.mu.Lock()
			status.State = "ready"
			if status.ConnectedAt.IsZero() {
				status.ConnectedAt = time.Now()
			}
			status.mu.Unlock()
			logger.Info("Feishu long connection ready", "event_type", strings.Join(types, ","))
		}),
		larkws.WithOnReconnecting(func() {
			status.mu.Lock()
			status.State = "reconnecting"
			status.mu.Unlock()
			logger.Warn("Feishu long connection reconnecting")
		}),
		larkws.WithOnReconnected(func() {
			status.mu.Lock()
			status.State = "ready"
			status.mu.Unlock()
			logger.Info("Feishu long connection reconnected")
		}),
		larkws.WithOnDisconnected(func() {
			status.mu.Lock()
			status.State = "disconnected"
			status.mu.Unlock()
			logger.Warn("Feishu long connection disconnected")
		}),
		larkws.WithOnError(func(err error) {
			status.mu.Lock()
			status.LastError = err.Error()
			status.mu.Unlock()
			logger.Error("Feishu long connection error", "error", err)
		}),
	)

	return &Listener{client: client, status: status}, nil
}

func newEventDispatcher(sinks map[string]Sink) (*dispatcher.EventDispatcher, []string, error) {
	if len(sinks) == 0 {
		return nil, nil, fmt.Errorf("Feishu listener requires event registrations")
	}
	types := make([]string, 0, len(sinks))
	for eventType := range sinks {
		if eventType == "" || eventType != strings.TrimSpace(eventType) || eventType == "app_ticket" {
			return nil, nil, fmt.Errorf("invalid or reserved Feishu event registration")
		}
		types = append(types, eventType)
	}
	sort.Strings(types)
	handler := dispatcher.NewEventDispatcher("", "")
	for _, eventType := range types {
		sink := sinks[eventType]
		handler.OnCustomizedEvent(eventType, func(ctx context.Context, req *larkevent.EventReq) error {
			if sink == nil {
				return nil
			}
			return sink(ctx, decodeEvent(req.Body, eventType))
		})
	}
	return handler, types, nil
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
	UUID  string `json:"uuid"`
	Type  string `json:"type"`
	Event struct {
		Type string `json:"type"`
	} `json:"event"`
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
		if envelope.Header.EventID == "" && envelope.Type == "event_callback" {
			event.ID = envelope.UUID
			if envelope.Event.Type != "" {
				event.Type = envelope.Event.Type
			}
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
		var scope struct {
			Event struct {
				Base  string `json:"file_token"`
				Table string `json:"table_id"`
			} `json:"event"`
		}
		if json.Unmarshal(event.Payload, &scope) == nil && scope.Event.Base != "" {
			attrs = append(attrs, "base_token", scope.Event.Base, "table_id", scope.Event.Table)
		}
		if logRaw {
			attrs = append(attrs, "payload", string(event.Payload))
		}
		logger.InfoContext(ctx, "Feishu event received", attrs...)
		return nil
	}
}
