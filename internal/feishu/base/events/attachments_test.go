package events

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
)

type changeCollector struct{ changes []recognition.Change }

func (c *changeCollector) EnqueueChange(_ context.Context, change recognition.Change) error {
	c.changes = append(c.changes, change)
	return nil
}

func TestAttachmentSinkAcceptsLiveAttachmentTokenAndEnqueuesOnlyNewFiles(t *testing.T) {
	consumer := &changeCollector{}
	sink := NewAttachmentSink(recognition.Config{BaseToken: "base", TableID: "table", FieldID: "attachment"}, consumer)
	// The enterprise record_edited event uses attachmentToken, unlike REST file_token.
	payload := []byte(`{"event":{"file_token":"base","table_id":"table","action_list":[{"action":"record_edited","record_id":"record","before_value":[{"field_id":"attachment","field_value":"[{\"attachmentToken\":\"old\",\"id\":\"cell-old\"}]"}],"after_value":[{"field_id":"attachment","field_value":"[{\"attachmentToken\":\"old\",\"id\":\"cell-old\"},{\"attachmentToken\":\"new\",\"id\":\"cell-new\"}]"}]}]}}`)
	if !json.Valid(payload) {
		t.Fatal("invalid test event")
	}
	if err := sink.Sink(context.Background(), Event{ID: "event", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(consumer.changes) != 1 {
		t.Fatalf("changes = %d, want one", len(consumer.changes))
	}
	change := consumer.changes[0]
	if change.EventID != "event" || change.RecordID != "record" || len(change.Tokens) != 1 || !change.Tokens["new"] {
		t.Fatalf("unexpected attachment change: %#v", change)
	}
}

func TestAttachmentTokensDoNotTreatCellIDAsDownloadToken(t *testing.T) {
	if _, ok := attachmentTokens(`[{"id":"cell-only"}]`); ok {
		t.Fatal("an attachment cell ID cannot replace a file token")
	}
	for _, value := range []string{`[{"file_token":"file"}]`, `[{"token":"file"}]`, `[{"attachmentToken":"file"}]`} {
		tokens, ok := attachmentTokens(value)
		if !ok || !tokens["file"] {
			t.Fatalf("unsupported known attachment representation: %s", value)
		}
	}
}
