package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Store abstracts persistent atomic key-value operations.
type Store interface {
	Transaction(ctx context.Context, key string, change func(json.RawMessage) (any, error)) error
}

// MemoryStore provides an in-memory implementation of Store for tests.
type MemoryStore struct {
	data map[string][]byte
}

// NewMemoryStore creates an in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]byte)}
}

func (m *MemoryStore) Transaction(ctx context.Context, key string, change func(json.RawMessage) (any, error)) error {
	raw := m.data[key]
	val, err := change(raw)
	if err != nil {
		return err
	}
	if val == nil {
		return nil
	}
	bytes, err := json.Marshal(val)
	if err != nil {
		return err
	}
	m.data[key] = bytes
	return nil
}

// Service manages task lifecycles, binding bitable records to task GUIDs and closing them on upload.
type Service struct {
	client *Client
	store  Store
}

// NewService instantiates a task service.
func NewService(client *Client, store Store) *Service {
	return &Service{
		client: client,
		store:  store,
	}
}

type recordTaskMeta struct {
	RecordID  string `json:"record_id"`
	TaskGUID  string `json:"task_guid"`
	Completed bool   `json:"completed"`
	CreatedAt int64  `json:"created_at"`
}

func taskKey(recordID string) string {
	return "task:record:" + strings.TrimSpace(recordID)
}

// LinkRecordTask persists the association between a reimbursement detail record and a task GUID.
func (s *Service) LinkRecordTask(ctx context.Context, recordID, taskGUID string) error {
	if s.store == nil || recordID == "" || taskGUID == "" {
		return nil
	}
	return s.store.Transaction(ctx, taskKey(recordID), func(raw json.RawMessage) (any, error) {
		return recordTaskMeta{
			RecordID:  recordID,
			TaskGUID:  taskGUID,
			Completed: false,
			CreatedAt: time.Now().Unix(),
		}, nil
	})
}

// GetTaskGUID retrieves the associated task GUID for a detail record ID, if present.
func (s *Service) GetTaskGUID(ctx context.Context, recordID string) (string, error) {
	if s.store == nil || recordID == "" {
		return "", nil
	}
	var guid string
	err := s.store.Transaction(ctx, taskKey(recordID), func(raw json.RawMessage) (any, error) {
		if len(raw) == 0 {
			return nil, nil
		}
		var meta recordTaskMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
		if !meta.Completed {
			guid = meta.TaskGUID
		}
		return nil, nil // read-only
	})
	return guid, err
}

// UnlinkRecordTask removes the association between a detail record ID and its task.
func (s *Service) UnlinkRecordTask(ctx context.Context, recordID string) error {
	if s.store == nil || recordID == "" {
		return nil
	}
	return s.store.Transaction(ctx, taskKey(recordID), func(raw json.RawMessage) (any, error) {
		return recordTaskMeta{
			RecordID:  recordID,
			TaskGUID:  "",
			Completed: true,
			CreatedAt: time.Now().Unix(),
		}, nil
	})
}

// CompleteOnInvoiceUpload automatically completes the linked task, writes a confirmation comment,
// and updates the task summary when an invoice is uploaded.
func (s *Service) CompleteOnInvoiceUpload(ctx context.Context, recordID, fileName, ledgerRecordID string) error {
	if s.client == nil || recordID == "" {
		return nil
	}

	taskGUID, err := s.GetTaskGUID(ctx, recordID)
	if err != nil {
		return fmt.Errorf("lookup task for record %s: %w", recordID, err)
	}
	if taskGUID == "" {
		return nil // No pending task linked to this record
	}

	// 1. Post a formal completion comment to the task comments list
	commentContent := fmt.Sprintf("【系统自动结单】已检测到发票凭证上传：\n• 上传文件：%s\n• 识别状态：已完成 OCR 提取并录入发票台账\n• 结单时间：%s\n系统已自动核销并完成任务，感谢配合！",
		fileName,
		time.Now().Format("2006-01-02 15:04:05"),
	)
	_ = s.client.CreateComment(ctx, taskGUID, commentContent) // Best-effort: comment permission might be pending

	// 2. Update summary to reflect completion and mark the task as completed
	_ = s.client.UpdateTask(ctx, taskGUID, map[string]any{
		"summary": fmt.Sprintf("[已自动结单] 发票凭证已上传 (%s)", fileName),
	}, []string{"summary"})

	if err := s.client.CompleteTask(ctx, taskGUID); err != nil {
		return fmt.Errorf("complete task %s: %w", taskGUID, err)
	}

	// 3. Clean up the link in store
	_ = s.UnlinkRecordTask(ctx, recordID)
	return nil
}
