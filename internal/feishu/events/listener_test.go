package events

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestDecodeEvent(t *testing.T) {
	payload := []byte(`{"header":{"event_id":"evt_123","event_type":"example.event"},"event":{}}`)
	event := decodeEvent(payload, "fallback.event")

	if event.ID != "evt_123" {
		t.Fatalf("ID = %q, want evt_123", event.ID)
	}
	if event.Type != "example.event" {
		t.Fatalf("Type = %q, want example.event", event.Type)
	}
	if !json.Valid(event.Payload) {
		t.Fatal("Payload is not valid JSON")
	}
}

func TestDecodeEventFallback(t *testing.T) {
	event := decodeEvent([]byte(`{"not":"an event"}`), "fallback.event")
	if event.Type != "fallback.event" {
		t.Fatalf("Type = %q, want fallback.event", event.Type)
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New("", "secret", DefaultEventType, nil, slog.Default()); err == nil {
		t.Fatal("New() error = nil, want credential error")
	}
}

func TestLoggingSink(t *testing.T) {
	sink := LoggingSink(slog.Default(), false)
	if err := sink(context.Background(), Event{Type: "example.event", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("LoggingSink() error = %v", err)
	}
}

func TestLoggingSinkRecordsScopeWithoutSecretsOrFieldValues(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	payload := []byte(`{"header":{"token":"private-verification-token"},"event":{"file_token":"base-1","table_id":"table-1","action_list":[{"field_value":"private-business-value"}]}}`)
	if err := LoggingSink(logger, false)(context.Background(), Event{ID: "evt-1", Type: DefaultEventType, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["base_token"] != "base-1" || fields["table_id"] != "table-1" || fields["event_id"] != "evt-1" {
		t.Fatalf("event scope missing from transport log: %#v", fields)
	}
	if strings.Contains(output.String(), "private-") {
		t.Fatal("transport log exposed a token or business field value")
	}
}
