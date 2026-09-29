package feishu

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAttachmentAllowlistUsesContentNotExtension(t *testing.T) {
	c := NewAttachmentClient("", "")
	c.httpClient.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("a fake PDF file")), Header: make(http.Header)}, nil
	})
	if _, _, err := c.downloadAndCheck(context.Background(), "https://example.com/invoice.pdf"); err == nil {
		t.Fatal("renamed fake PDF was accepted")
	}
}
