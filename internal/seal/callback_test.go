package seal

import (
	"strings"
	"testing"
)

func TestDecodeCallback_StandardCamelCase(t *testing.T) {
	raw := []byte(`{
		"documentId": "doc-123",
		"approvalRecordId": "app-456",
		"decision": "approve",
		"comment": "all valid",
		"approvalUrl": "https://seal.example.com/app/456"
	}`)

	docID, outcome, err := DecodeCallback(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if docID != "doc-123" {
		t.Errorf("expected doc-123, got %s", docID)
	}
	if outcome.Decision != "approve" {
		t.Errorf("expected approve, got %s", outcome.Decision)
	}
	if outcome.Comment != "all valid" {
		t.Errorf("expected 'all valid', got %s", outcome.Comment)
	}
	if outcome.ExternalID != "app-456" {
		t.Errorf("expected app-456, got %s", outcome.ExternalID)
	}
	if outcome.URL != "https://seal.example.com/app/456" {
		t.Errorf("expected url, got %s", outcome.URL)
	}
}

func TestDecodeCallback_SnakeCaseAndCaseInsensitiveDecision(t *testing.T) {
	raw := []byte(`{
		"document_id": "doc-snake-1",
		"approval_record_id": "app-snake-2",
		"decision": " REJECT ",
		"comment": "tax rate mismatch",
		"approval_url": "https://seal.example.com/app/snake"
	}`)

	docID, outcome, err := DecodeCallback(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if docID != "doc-snake-1" {
		t.Errorf("expected doc-snake-1, got %s", docID)
	}
	if outcome.Decision != "reject" {
		t.Errorf("expected reject, got %s", outcome.Decision)
	}
	if outcome.ExternalID != "app-snake-2" {
		t.Errorf("expected app-snake-2, got %s", outcome.ExternalID)
	}
	if outcome.URL != "https://seal.example.com/app/snake" {
		t.Errorf("expected url, got %s", outcome.URL)
	}
}

func TestDecodeCallback_InvalidInputs(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name:    "missing documentId",
			json:    `{"approvalRecordId":"app-1","decision":"approve"}`,
			wantErr: "invalid Seal callback identity",
		},
		{
			name:    "missing approvalRecordId",
			json:    `{"documentId":"doc-1","decision":"approve"}`,
			wantErr: "invalid Seal callback identity",
		},
		{
			name:    "invalid decision value",
			json:    `{"documentId":"doc-1","approvalRecordId":"app-1","decision":"unknown"}`,
			wantErr: "invalid review decision",
		},
		{
			name:    "malformed json",
			json:    `{not valid json`,
			wantErr: "invalid Seal callback json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := DecodeCallback([]byte(tt.json))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}
