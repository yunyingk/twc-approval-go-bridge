package events

import (
	"context"
	"errors"
	"testing"
)

type reviewChangeCollector struct {
	records []string
	events  []string
	fail    bool
}

func (c *reviewChangeCollector) NotifyChange(_ context.Context, recordID, eventID string) error {
	if c.fail {
		return errors.New("disk unavailable")
	}
	c.records = append(c.records, recordID)
	c.events = append(c.events, eventID)
	return nil
}

func TestReviewChangesFilterEffectiveInputsAndPreserveDeletion(t *testing.T) {
	c := &reviewChangeCollector{}
	sink, err := NewReviewChangeSink("base", "details", []string{"reason", "attachment", "payment"}, c)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"event":{"file_token":"base","table_id":"details","action_list":[
	{"action":"record_edited","record_id":"ai","after_value":[{"field_id":"decision","field_value":"approve"}]},
	{"action":"record_edited","record_id":"workflow","after_value":[{"field_id":"locked","field_value":"true"}]},
	{"action":"record_edited","record_id":"unchanged","before_value":[{"field_id":"reason","field_value":"same"}],"after_value":[{"field_id":"reason","field_value":"same"}]},
	{"action":"record_edited","record_id":"input","before_value":[{"field_id":"reason","field_value":"old"}],"after_value":[{"field_id":"reason","field_value":"new"}]},
	{"action":"record_edited","record_id":"cleared","before_value":[{"field_id":"attachment","field_value":"[old]"}],"after_value":[{"field_id":"attachment","field_value":"[]"}]},
	{"action":"record_edited","record_id":"unlinked","before_value":[{"field_id":"payment","field_value":"[old]"}]},
	{"action":"record_deleted","record_id":"removed"},
	{"action":"unsupported","record_id":"ignored","after_value":[{"field_id":"reason","field_value":"changed"}]}
	]}}`)
	if err := sink.Sink(context.Background(), Event{ID: "event", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(c.records) != 4 || c.records[0] != "input" || c.records[1] != "cleared" || c.records[2] != "unlinked" || c.records[3] != "removed" {
		t.Fatalf("unexpected changes: %v", c.records)
	}
	for _, id := range c.events {
		if id != "event" {
			t.Fatal("event identity lost")
		}
	}
	if err := sink.Sink(context.Background(), Event{Payload: []byte(`{"event":{"file_token":"other-base","table_id":"details","action_list":[{"action":"record_deleted","record_id":"other"}]}}`)}); err != nil || len(c.records) != 4 {
		t.Fatal("another source was processed")
	}
	c.fail = true
	if err := sink.Sink(context.Background(), Event{ID: "retry", Payload: payload}); err == nil {
		t.Fatal("event was acknowledged without durable intent")
	}
}
