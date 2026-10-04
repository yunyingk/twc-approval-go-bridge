package base

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type ledgerTransport struct {
	created        bool
	writes         []map[string]any
	existingFields map[string]any
}

func (t *ledgerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response := `{"code":0,"data":{}}`
	switch {
	case req.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
		response = `{"code":0,"tenant_access_token":"test-token"}`
	case strings.HasSuffix(req.URL.Path, "/fields"):
		response = `{"code":0,"data":{"items":[{"field_id":"source-id","field_name":"识别来源键","type":1},{"field_id":"raw-id","field_name":"识别原始JSON","type":1},{"field_id":"rate-id","field_name":"税率","type":2},{"field_id":"text-rate-id","field_name":"文本税率","type":1}],"has_more":false}}`
	case strings.HasSuffix(req.URL.Path, "/records/search"):
		if t.created {
			response = `{"code":0,"data":{"items":[{"record_id":"record-1"}],"has_more":false}}`
			if t.existingFields != nil {
				raw, err := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"record_id": "record-1", "fields": t.existingFields}}, "has_more": false}})
				if err != nil {
					return nil, err
				}
				response = string(raw)
			}
		} else {
			response = `{"code":0,"data":{"items":[],"has_more":false}}`
		}
	case strings.HasSuffix(req.URL.Path, "/records") && req.Method == http.MethodPost:
		if req.URL.Query().Get("client_token") == "" {
			return nil, io.ErrUnexpectedEOF
		}
		var body struct {
			Fields map[string]any `json:"fields"`
		}
		decoder := json.NewDecoder(req.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			return nil, err
		}
		t.writes = append(t.writes, body.Fields)
		t.created = true
		response = `{"code":0,"data":{"record":{"record_id":"record-1"}}}`
	case strings.HasSuffix(req.URL.Path, "/records/record-1") && req.Method == http.MethodPut:
		var body struct {
			Fields map[string]any `json:"fields"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		t.writes = append(t.writes, body.Fields)
		response = `{"code":0,"data":{"record":{"record_id":"record-1"}}}`
	default:
		return nil, io.ErrUnexpectedEOF
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestLedgerUpsertWritesNumericTextAsNumberOnlyForNumberFields(t *testing.T) {
	transport := &ledgerTransport{}
	client := NewLedgerClient("app", "secret")
	client.httpClient.Transport = transport
	_, _, err := client.UpsertLedgerRecord(context.Background(), "base", "ledger", "source-id", "rec:file", map[string]any{
		"rate-id": "0.06", "text-rate-id": "0.06", "raw-id": `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := transport.writes[0]
	if rate, ok := fields["税率"].(json.Number); !ok || rate.String() != "0.06" || fields["文本税率"] != "0.06" {
		t.Fatalf("number and text columns lost their types: %#v", fields)
	}
}

func TestLedgerUpsertRejectsAmbiguousNumberWithoutWriting(t *testing.T) {
	for _, value := range []string{"6%", "1,23"} {
		t.Run(value, func(t *testing.T) {
			transport := &ledgerTransport{}
			client := NewLedgerClient("app", "secret")
			client.httpClient.Transport = transport
			_, _, err := client.UpsertLedgerRecord(context.Background(), "base", "ledger", "source-id", "rec:file", map[string]any{"rate-id": value})
			if err == nil || len(transport.writes) != 0 {
				t.Fatalf("ambiguous number must not reach a write: error=%v writes=%d", err, len(transport.writes))
			}
		})
	}
}

func TestRecognizedInvoicePreservesHumanNumberBeforeValidatingOCR(t *testing.T) {
	transport := &ledgerTransport{created: true, existingFields: map[string]any{"税率": json.Number("0.12")}}
	client := NewLedgerClient("app", "secret")
	client.httpClient.Transport = transport
	_, created, err := client.UpsertRecognizedInvoice(context.Background(), "base", "ledger", "source-id", "rec:file", map[string]any{"rate-id": "6%"})
	if err != nil || created {
		t.Fatalf("existing human value should take precedence: created=%v error=%v", created, err)
	}
	if _, overwritten := transport.writes[0]["税率"]; overwritten {
		t.Fatal("OCR must not overwrite the existing human tax rate")
	}
}

func TestLedgerUpsertCreatesThenUpdatesBySourceKey(t *testing.T) {
	transport := &ledgerTransport{}
	client := NewLedgerClient("app", "secret")
	client.httpClient.Transport = transport
	for attempt := 0; attempt < 2; attempt++ {
		id, created, err := client.UpsertLedgerRecord(context.Background(), "base", "ledger", "source-id", "rec:file", map[string]any{"raw-id": `{}`})
		if err != nil {
			t.Fatal(err)
		}
		if id != "record-1" || created != (attempt == 0) {
			t.Fatalf("attempt %d: id=%q created=%v", attempt, id, created)
		}
	}
	if len(transport.writes) != 2 {
		t.Fatalf("got %d writes, want 2", len(transport.writes))
	}
	for _, fields := range transport.writes {
		if fields["识别来源键"] != "rec:file" || fields["识别原始JSON"] != `{}` {
			t.Fatalf("unexpected fields: %v", fields)
		}
	}
}
