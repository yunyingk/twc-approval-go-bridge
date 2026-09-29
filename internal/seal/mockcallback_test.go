package seal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockCallbackOnlyAcknowledgesLocalValidDecisions(t *testing.T) {
	handler := MockCallback(nil)
	request := httptest.NewRequest(http.MethodPost, "/seal/callback/mock", strings.NewReader(`{"documentId":"d","approvalRecordId":"a","decision":"review"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"success":true}` {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	request.RemoteAddr = "203.0.113.1:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("nonlocal response = %d", response.Code)
	}
}
