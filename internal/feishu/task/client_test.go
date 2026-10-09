package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTaskClient_Flow(t *testing.T) {
	var gotTaskPayload map[string]any
	var gotPatchPayload map[string]any
	var gotCommentPayload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "mock-token",
			})
			return
		}

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/task/v2/tasks") {
			_ = json.NewDecoder(r.Body).Decode(&gotTaskPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"task": map[string]any{
						"guid": "mock-task-guid-123",
					},
				},
			})
			return
		}

		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/task/v2/tasks/mock-task-guid-123") {
			_ = json.NewDecoder(r.Body).Decode(&gotPatchPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
			})
			return
		}

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/task/v2/comments") {
			_ = json.NewDecoder(r.Body).Decode(&gotCommentPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient("mock-app-id", "mock-app-secret")
	client.baseURL = server.URL

	ctx := context.Background()
	startTime := time.UnixMilli(1791350400000)
	dueTime := time.UnixMilli(1791955200000)

	// 1. CreateTask
	guid, err := client.CreateTask(ctx, CreateTaskParam{
		Summary:        "Test Task",
		Description:    "Test Description",
		AssigneeOpenID: "ou_test_user",
		StartTime:      &startTime,
		DueTime:        &dueTime,
		OriginTitle:    "Test Link",
		OriginURL:      "https://example.com/record/1",
	})
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if guid != "mock-task-guid-123" {
		t.Fatalf("expected guid mock-task-guid-123, got: %s", guid)
	}
	if gotTaskPayload["summary"] != "Test Task" {
		t.Errorf("unexpected summary: %v", gotTaskPayload["summary"])
	}

	// 2. CompleteTask
	if err := client.CompleteTask(ctx, guid); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}
	taskMap, ok := gotPatchPayload["task"].(map[string]any)
	if !ok || taskMap["completed_at"] == "" {
		t.Errorf("expected completed_at in patch payload, got: %v", gotPatchPayload)
	}

	// 3. CreateComment
	if err := client.CreateComment(ctx, guid, "Test Comment Content"); err != nil {
		t.Fatalf("CreateComment failed: %v", err)
	}
	if gotCommentPayload["content"] != "Test Comment Content" {
		t.Errorf("unexpected comment content: %v", gotCommentPayload["content"])
	}
}
