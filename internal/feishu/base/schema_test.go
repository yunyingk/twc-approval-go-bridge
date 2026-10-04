package base

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type schemaTransport struct {
	pages int
}

func (s *schemaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response := ""
	switch {
	case strings.HasSuffix(req.URL.Path, "/tenant_access_token/internal") && req.Method == http.MethodPost:
		response = `{"code":0,"tenant_access_token":"test-token"}`
	case strings.HasSuffix(req.URL.Path, "/fields") && req.Method == http.MethodGet:
		if req.Header.Get("Authorization") != "Bearer test-token" {
			return nil, fmt.Errorf("missing app token")
		}
		s.pages++
		if req.URL.Query().Get("page_token") == "" {
			response = `{"code":0,"data":{"items":[{"field_id":"attachment","field_name":"附件","type":17}],"has_more":true,"page_token":"next"}}`
		} else if req.URL.Query().Get("page_token") == "next" {
			response = `{"code":0,"data":{"items":[{"field_id":"relation","field_name":"关联","type":21,"property":{"table_id":"ledger"}}],"has_more":false}}`
		}
	default:
		return nil, fmt.Errorf("inspection must not read or write records: %s %s", req.Method, req.URL.Path)
	}
	if response == "" {
		return nil, fmt.Errorf("unexpected pagination")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestInspectFieldsPaginatesWithAppIdentityAndRetainsLinkTarget(t *testing.T) {
	transport := &schemaTransport{}
	client := NewLedgerClient("app", "secret")
	client.httpClient.Transport = transport
	fields, err := client.InspectFields(context.Background(), "base", "details")
	if err != nil {
		t.Fatal(err)
	}
	if transport.pages != 2 || fields["attachment"].Type != 17 || fields["relation"].RelatedTableID != "ledger" {
		t.Fatalf("incomplete schema: pages=%d fields=%v", transport.pages, fields)
	}
}
