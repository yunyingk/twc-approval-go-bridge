package flow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
)

type testReader struct{}

func (testReader) ReadAttachments(context.Context, string, string, string, string, map[string]bool) ([]receipt.Attachment, []string, error) {
	return []receipt.Attachment{{Name: "new.png", ContentType: "image/png"}, {Name: "old.png", ContentType: "image/png"}}, []string{"new", "old"}, nil
}

type testRecognizer struct{}

func (testRecognizer) Recognize(context.Context, receipt.Attachment) (receipt.Recognition, error) {
	return receipt.Recognition{Outputs: map[string]json.RawMessage{"currency": json.RawMessage(`"USD"`)}}, nil
}

type testScanner struct {
	calls chan struct{}
	count int
}

func (s *testScanner) ScanAttachmentRecords(ctx context.Context, base, table, field string, yield func(string, map[string]bool) error) error {
	if base != "base" || table != "table" || field != "attachment" {
		return context.Canceled
	}
	s.count++
	tokens := map[string]bool{"old": true}
	if s.count > 1 {
		tokens["new"] = true
	}
	if err := yield("rec", tokens); err != nil {
		return err
	}
	select {
	case s.calls <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func TestPollUsesSameDeduplicationAsEvents(t *testing.T) {
	results := make(chan Result, 2)
	p, err := New(Config{BaseToken: "base", TableID: "table", FieldID: "attachment"}, testReader{}, testRecognizer{}, func(_ context.Context, r Result) error { results <- r; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	calls := make(chan struct{}, 2)
	go p.Poll(ctx, &testScanner{calls: calls}, 10*time.Millisecond, false)
	for range 2 {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatal("poll did not scan twice")
		}
	}
	select {
	case result := <-results:
		if result.RecordID != "rec" || result.FileToken != "new" {
			t.Fatalf("unexpected result: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("poll did not recognize attachment")
	}
	select {
	case result := <-results:
		t.Fatalf("duplicate result after rescan: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPollCanProcessExistingAttachments(t *testing.T) {
	results := make(chan Result, 1)
	p, err := New(Config{BaseToken: "base", TableID: "table", FieldID: "attachment"}, testReader{}, testRecognizer{}, func(_ context.Context, r Result) error { results <- r; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	go p.Poll(ctx, &testScanner{calls: make(chan struct{}, 1)}, time.Hour, true)
	select {
	case result := <-results:
		if result.FileToken != "old" || result.Trigger != "poll" {
			t.Fatalf("unexpected result: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("startup scan did not process existing attachment")
	}
}

func TestAttachmentChangeRecognizesOnlyNewFile(t *testing.T) {
	results := make(chan Result, 2)
	p, err := New(Config{BaseToken: "base", TableID: "table", FieldID: "attachment"}, testReader{}, testRecognizer{}, func(_ context.Context, r Result) error { results <- r; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	event := events.Event{ID: "evt", Payload: json.RawMessage(`{"event":{"file_token":"base","table_id":"table","action_list":[{"action":"record_edited","record_id":"rec","before_value":[{"field_id":"attachment","field_value":"[{\"file_token\":\"old\"}]"}],"after_value":[{"field_id":"attachment","field_value":"[{\"file_token\":\"old\"},{\"file_token\":\"new\"}]"}]}]}}`)}
	if err := p.Sink(ctx, event); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if result.FileToken != "new" || result.RecordID != "rec" || string(result.Recognition.Outputs["currency"]) != `"USD"` {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("recognition was not delivered")
	}
	if err := p.Sink(ctx, event); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		t.Fatalf("duplicate recognition: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	event.Payload = json.RawMessage(`{"event":{"file_token":"different","table_id":"table"}}`)
	if err := p.Sink(ctx, event); err != nil {
		t.Fatal(err)
	}
	if len(p.jobs) != 0 {
		t.Fatal("wrong Base event was queued")
	}
}
