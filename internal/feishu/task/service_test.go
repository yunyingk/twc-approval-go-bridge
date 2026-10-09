package task

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestService_LinkAndComplete(t *testing.T) {
	var completed bool
	var commented bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"mock-token"}`))
			return
		}
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/task/v2/tasks/task-guid-456") {
			completed = true
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok"}`))
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/task/v2/comments") {
			commented = true
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient("mock-app", "mock-secret")
	client.baseURL = server.URL

	store := NewMemoryStore()
	svc := NewService(client, store)
	ctx := context.Background()

	recordID := "rec-detail-123"
	taskGUID := "task-guid-456"

	// 1. Link
	if err := svc.LinkRecordTask(ctx, recordID, taskGUID); err != nil {
		t.Fatalf("LinkRecordTask failed: %v", err)
	}

	gotGUID, err := svc.GetTaskGUID(ctx, recordID)
	if err != nil || gotGUID != taskGUID {
		t.Fatalf("expected %s, got %s (err: %v)", taskGUID, gotGUID, err)
	}

	// 2. Complete on upload
	if err := svc.CompleteOnInvoiceUpload(ctx, recordID, "receipt.pdf", "ledger-1"); err != nil {
		t.Fatalf("CompleteOnInvoiceUpload failed: %v", err)
	}

	if !completed {
		t.Errorf("expected CompleteTask to be called")
	}
	if !commented {
		t.Errorf("expected CreateComment to be called")
	}

	// 3. Link should be cleared
	afterGUID, err := svc.GetTaskGUID(ctx, recordID)
	if err != nil || afterGUID != "" {
		t.Errorf("expected unlinked, got: %s", afterGUID)
	}
}
