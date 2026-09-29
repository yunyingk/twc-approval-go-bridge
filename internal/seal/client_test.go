package seal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUploadAttachment(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/integrations/webhook/test/attachments" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		files := r.MultipartForm.File["files"]
		if len(files) != 1 || files[0].Filename != "receipt.png" {
			t.Errorf("files = %#v", files)
			return
		}
		file, err := files[0].Open()
		if err != nil {
			t.Errorf("open upload: %v", err)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if string(data) != "image-data" || files[0].Header.Get("Content-Type") != "image/png" {
			t.Errorf("uploaded data or type differs")
		}
		_, _ = io.WriteString(w, `{"data":{"attachments":[{"attachmentId":"att-1","name":"receipt.png","mimeType":"image/png","ossPath":"path","ossSignedUrl":"signed","ossFileSize":10}]}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{DocumentURL: server.URL + "/api/v1/integrations/webhook/test/document", BearerToken: "test-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.UploadAttachment(context.Background(), Attachment{Name: "receipt.png", ContentType: "image/png", Data: []byte("image-data")})
	if err != nil {
		t.Fatal(err)
	}
	if response.AttachmentID != "att-1" || response.Attachment.OSSPath != "path" || response.Attachment.OSSSignedURL != "signed" || response.Attachment.OSSFileSize != 10 {
		t.Fatalf("unexpected upload response: %#v", response)
	}
}

func TestUploadAttachmentSingleID(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"attachmentId":"att-2"}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{DocumentURL: server.URL + "/document", BearerToken: "test-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.UploadAttachment(context.Background(), Attachment{Data: []byte("x")})
	if err != nil || response.AttachmentID != "att-2" {
		t.Fatalf("upload = %#v, %v", response, err)
	}
}

func TestUploadAttachmentTopLevelMetadata(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"attachmentId":"att-3","name":"receipt.jpg","mimeType":"image/jpeg","url":"https://example.test/file","ossPath":"p","ossSignedUrl":"https://example.test/signed","ossFileSize":12}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{DocumentURL: server.URL + "/document", BearerToken: "test-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.UploadAttachment(context.Background(), Attachment{Data: []byte("x")})
	if err != nil || response.AttachmentID != "att-3" || response.Attachment.Name != "receipt.jpg" || response.Attachment.OSSFileSize != 12 {
		t.Fatalf("upload = %#v, %v", response, err)
	}
}

func TestSubmitDocument(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/document" || r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected submit request")
		}
		var document struct {
			DocumentID string `json:"documentId"`
			StartTime  int64  `json:"startTime"`
			Invoices   []struct {
				TotalAmount json.Number `json:"totalAmount"`
			} `json:"invoices"`
		}
		if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if document.DocumentID != "doc-1" || document.StartTime != 123 || len(document.Invoices) != 1 || document.Invoices[0].TotalAmount != "12.34" {
			t.Errorf("unexpected document: %#v", document)
		}
		_, _ = io.WriteString(w, `{"data":{"success":true,"documentId":"doc-1","acceptedInvoices":[{"sourceInvoiceId":"invoice-1","status":"reused"}]}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{DocumentURL: server.URL + "/document", BearerToken: "test-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.SubmitDocument(context.Background(), DocumentRequest{
		DocumentID: "doc-1", DocumentSN: "SN-1", StartTime: 123,
		Fields:   []DocumentField{{Key: "source", Label: "来源", Type: "TEXT", Value: "test"}},
		Invoices: []ExternalInvoice{{Kind: "invoice", SourceInvoiceID: "invoice-1", InvoiceType: "overseas", CurrencyCode: "USD", TotalAmount: "12.34", EvidenceAttachmentID: "att-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.DocumentID != "doc-1" || len(response.AcceptedInvoices) != 1 || response.AcceptedInvoices[0].Status != "reused" {
		t.Fatalf("unexpected submit response: %#v", response)
	}
}

func TestErrorsDoNotExposeProviderBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"secret":"test-secret","receipt":"private receipt data"}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{DocumentURL: server.URL + "/document", BearerToken: "test-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SubmitDocument(context.Background(), DocumentRequest{DocumentID: "doc", DocumentSN: "sn", StartTime: 1, Fields: []DocumentField{}})
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status error = %v", err)
	}
	if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "private receipt data") {
		t.Fatalf("sensitive data in error: %v", err)
	}
}

func TestRedirectDoesNotForwardToken(t *testing.T) {
	t.Parallel()
	var forwarded atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Store(true) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := NewClient(Config{DocumentURL: server.URL + "/document", BearerToken: "test-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SubmitDocument(context.Background(), DocumentRequest{DocumentID: "doc", DocumentSN: "sn", StartTime: 1, Fields: []DocumentField{}})
	if err == nil || forwarded.Load() {
		t.Fatalf("redirect was followed: err=%v forwarded=%v", err, forwarded.Load())
	}
}

func TestClientConfigAndTimeout(t *testing.T) {
	t.Parallel()
	for _, documentURL := range []string{"", "https://example.com/other", "https://user:pass@example.com/document", "https://example.com/document?token=x"} {
		if _, err := NewClient(Config{DocumentURL: documentURL, BearerToken: "secret"}, nil); err == nil {
			t.Errorf("accepted invalid URL %q", documentURL)
		}
	}
	if _, err := NewClient(Config{DocumentURL: "https://example.com/document"}, nil); err == nil {
		t.Fatal("accepted missing Bearer token")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = io.WriteString(w, `{"data":{"success":true}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{DocumentURL: server.URL + "/document", BearerToken: "secret"}, &http.Client{Timeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SubmitDocument(context.Background(), DocumentRequest{DocumentID: "doc", DocumentSN: "sn", StartTime: 1, Fields: []DocumentField{}})
	if err == nil {
		t.Fatal("expected deadline error")
	}
}
