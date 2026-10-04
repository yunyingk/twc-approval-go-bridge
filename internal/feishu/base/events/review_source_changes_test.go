package events

import (
	"context"
	"errors"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
)

type sourceChangeCollector struct {
	changes []app.SourceChange
	fail    bool
}

func (c *sourceChangeCollector) EnqueueSourceChange(_ context.Context, change app.SourceChange) error {
	if c.fail {
		return errors.New("disk full")
	}
	c.changes = append(c.changes, change)
	return nil
}

func TestSourceEventFiltersWorkflowAndCarriesBeforeAndAfterNumbers(t *testing.T) {
	c := &sourceChangeCollector{}
	sink, err := NewReviewSourceChangeSink("scope", []ReviewSourceBinding{{Kind: app.InvoiceSource, BaseToken: "base", TableID: "ledger", FieldIDs: []string{"number", "total", "raw"}, InvoiceNumberFieldID: "number"}}, c)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"event":{"file_token":"base","table_id":"ledger","action_list":[
	{"action":"record_edited","record_id":"ignored","after_value":[{"field_id":"locked","field_value":"true"}]},
	{"action":"record_edited","record_id":"invoice","before_value":[{"field_id":"number","field_value":"[{\"text\":\"OLD\"}]"}],"after_value":[{"field_id":"number","field_value":"\"NEW\""}]},
	{"action":"record_edited","record_id":"invoice","before_value":[{"field_id":"total","field_value":"10"}],"after_value":[{"field_id":"total","field_value":"20"}]},
	{"action":"record_edited","record_id":"same","before_value":[{"field_id":"total","field_value":"10"}],"after_value":[{"field_id":"total","field_value":"10"}]}
	]}}`)
	if err := sink.Sink(context.Background(), Event{ID: "event", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(c.changes) != 1 {
		t.Fatalf("unexpected source changes: %v", c.changes)
	}
	change := c.changes[0]
	if change.Scope != "scope" || change.Source != "feishu:base:ledger" || change.RecordID != "invoice" || change.EventID != "event" || change.AllDetails || len(change.InvoiceNumbers) != 2 || change.InvoiceNumbers[0] != "OLD" || change.InvoiceNumbers[1] != "NEW" {
		t.Fatalf("source hints or identity lost: %+v", change)
	}
	c.fail = true
	if err := sink.Sink(context.Background(), Event{ID: "retry", Payload: payload}); err == nil {
		t.Fatal("failed persistence was acknowledged")
	}
}

func TestSourceEventUnknownHintsWidenScopeAndPaymentsKeepOnlyIdentity(t *testing.T) {
	c := &sourceChangeCollector{}
	sink, err := NewReviewSourceChangeSink("scope", []ReviewSourceBinding{
		{Kind: app.InvoiceSource, BaseToken: "base", TableID: "ledger", FieldIDs: []string{"number"}, InvoiceNumberFieldID: "number"},
		{Kind: app.TransactionSource, BaseToken: "base", TableID: "payments", FieldIDs: []string{"amount", "status"}},
	}, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{"event":{"file_token":"base","table_id":"ledger","action_list":[{"action":"record_added","record_id":"new"}]}}`,
		`{"event":{"file_token":"base","table_id":"ledger","action_list":[{"action":"record_deleted","record_id":"removed"}]}}`,
		`{"event":{"file_token":"base","table_id":"ledger","action_list":[{"action":"record_edited","record_id":"unknown","after_value":[{"field_id":"number","field_value":"{\"unsupported\":\"value\"}"}]}]}}`,
		`{"event":{"file_token":"base","table_id":"payments","action_list":[{"action":"record_edited","record_id":"payment","after_value":[{"field_id":"amount","field_value":"999999999999999999.01"}]},{"action":"record_edited","record_id":"claim","after_value":[{"field_id":"claim_status","field_value":"done"}]}]}}`,
	} {
		if err := sink.Sink(context.Background(), Event{ID: "event", Payload: []byte(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.changes) != 4 {
		t.Fatalf("unexpected source notices: %v", c.changes)
	}
	for _, change := range c.changes[:3] {
		if !change.AllDetails {
			t.Fatal("unknown ledger hints dropped candidate changes")
		}
	}
	payment := c.changes[3]
	if payment.Kind != app.TransactionSource || payment.AllDetails || len(payment.InvoiceNumbers) != 0 || payment.RecordID != "payment" {
		t.Fatal("payment values escaped into invalidation evidence")
	}
	if err := sink.Sink(context.Background(), Event{Payload: []byte(`{"event":{"file_token":"other-base","table_id":"ledger","action_list":[{"action":"record_deleted","record_id":"foreign"}]}}`)}); err != nil || len(c.changes) != 4 {
		t.Fatal("another Base was processed")
	}
}

func TestInvoiceNumberEventTextDoesNotGuessUnknownFormats(t *testing.T) {
	for _, value := range []string{`[{"id":"text-cell"}]`, `{"text":"INV"}`, `123`, `not-json`} {
		if _, ok := eventText(value); ok {
			t.Fatalf("unknown event form accepted: %s", value)
		}
	}
	for _, value := range []string{`null`, `[]`, `""`} {
		if text, ok := eventText(value); !ok || text != "" {
			t.Fatalf("empty field misunderstood: %s", value)
		}
	}
}
