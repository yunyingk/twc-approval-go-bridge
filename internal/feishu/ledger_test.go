package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type ledgerTransport struct {
	created bool
	writes  []map[string]any
}

func (t *ledgerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response := `{"code":0,"data":{}}`
	switch {
	case req.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
		response = `{"code":0,"tenant_access_token":"test-token"}`
	case strings.HasSuffix(req.URL.Path, "/fields"):
		response = `{"code":0,"data":{"items":[{"field_id":"source-id","field_name":"识别来源键"},{"field_id":"raw-id","field_name":"识别原始JSON"}],"has_more":false}}`
	case strings.HasSuffix(req.URL.Path, "/records/search"):
		if t.created {
			response = `{"code":0,"data":{"items":[{"record_id":"record-1"}],"has_more":false}}`
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
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
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
