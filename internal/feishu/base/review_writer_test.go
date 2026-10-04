package base

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type reviewWriterTransport struct {
	decisionType int
	calls        int
	writes       []map[string]string
}

func (t *reviewWriterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	response := `{"code":0,"data":{}}`
	switch {
	case strings.HasSuffix(req.URL.Path, "/tenant_access_token/internal"):
		response = `{"code":0,"tenant_access_token":"test-token"}`
	case strings.HasSuffix(req.URL.Path, "/fields"):
		items := []map[string]any{{"field_id": "human", "field_name": "审核状态", "type": 3}}
		for _, semantic := range []string{"decision", "comment", "document_id", "revision", "provider", "external_id", "url"} {
			fieldType := 1
			if semantic == "decision" {
				fieldType = t.decisionType
			}
			items = append(items, map[string]any{"field_id": semantic + "-id", "field_name": "AI-" + semantic, "type": fieldType})
		}
		raw, err := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": items, "has_more": false}})
		if err != nil {
			return nil, err
		}
		response = string(raw)
	case strings.HasSuffix(req.URL.Path, "/records/rec") && req.Method == http.MethodPut:
		var body struct {
			Fields map[string]string `json:"fields"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		t.writes = append(t.writes, body.Fields)
	default:
		return nil, io.ErrUnexpectedEOF
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
}

func reviewWriterFixture(t *testing.T, fieldType int) (*ReviewWriter, *reviewWriterTransport, core.Request) {
	t.Helper()
	transport := &reviewWriterTransport{decisionType: fieldType}
	client := NewLedgerClient("app", "secret")
	client.httpClient.Transport = transport
	fields := map[string]string{}
	for _, semantic := range []string{"decision", "comment", "document_id", "revision", "provider", "external_id", "url"} {
		fields[semantic] = semantic + "-id"
	}
	writer, err := NewReviewWriter(client, "base", "details", fields)
	if err != nil {
		t.Fatal(err)
	}
	request := core.Request{LogicalID: "feishu:base:details:rec", Revision: "revision", Provider: "seal",
		Document: aggregate.Document{RecordID: "rec", DocumentID: "feishu:base:details:rec:v:revision"}}
	return writer, transport, request
}

func TestReviewWriterWritesOnlyDedicatedResultFields(t *testing.T) {
	w, transport, request := reviewWriterFixture(t, 1)
	outcome := core.Outcome{Decision: "approve", Comment: "AI advice", ExternalID: "external", URL: "https://example.test/review"}
	if err := w.WriteReviewResult(context.Background(), request, outcome); err != nil {
		t.Fatal(err)
	}
	if len(transport.writes) != 1 {
		t.Fatalf("writes = %d", len(transport.writes))
	}
	fields := transport.writes[0]
	if len(fields) != 7 || fields["AI-decision"] != "approve" || fields["AI-document_id"] != request.Document.DocumentID ||
		fields["AI-revision"] != request.Revision || fields["AI-provider"] != "seal" || fields["AI-external_id"] != "external" ||
		fields["AI-comment"] != outcome.Comment || fields["AI-url"] != outcome.URL {
		t.Fatalf("incomplete result mapping: %#v", fields)
	}
	if _, exists := fields["审核状态"]; exists {
		t.Fatal("AI suggestion must not write human approval state")
	}
}

func TestReviewWriterRejectsNonTextColumnsBeforeWrite(t *testing.T) {
	for _, fieldType := range []int{2, 3, 20} {
		w, transport, request := reviewWriterFixture(t, fieldType)
		if err := w.WriteReviewResult(context.Background(), request, core.Outcome{Decision: "review"}); err == nil || len(transport.writes) != 0 {
			t.Fatalf("field type %d reached write: error=%v", fieldType, err)
		}
	}
}

func TestReviewWriterRejectsAnotherSourceWithoutAPICalls(t *testing.T) {
	w, transport, request := reviewWriterFixture(t, 1)
	request.LogicalID = "feishu:other-base:details:rec"
	if err := w.WriteReviewResult(context.Background(), request, core.Outcome{Decision: "approve"}); err == nil || transport.calls != 0 {
		t.Fatalf("another source reached API: error=%v calls=%d", err, transport.calls)
	}
}
