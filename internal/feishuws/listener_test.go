package feishuws

import (
	"context"
	"encoding/json"
	"log/slog"
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
