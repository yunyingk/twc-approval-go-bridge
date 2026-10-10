package audit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAuditFileStore_RecordAndTimeline(t *testing.T) {
	tempDir := t.TempDir()
	store, err := InitFileStore(tempDir)
	if err != nil {
		t.Fatalf("InitFileStore failed: %v", err)
	}

	ctx := context.Background()

	// 1. 模拟记录 1：什么时候推送了谁
	now := time.Now()
	err = store.Record(ctx, Entry{
		Timestamp:    now.Add(-10 * time.Minute),
		Type:         TypeNotifyCardSent,
		RecordID:     "rec_tx_1001",
		Operator:     "ou_user_123",
		OperatorName: "张三",
		Action:       "向持卡人张三推送补票卡片",
		Details: map[string]any{
			"merchant": "Apple Retail",
			"amount":   "1299.00 USD",
		},
	})
	if err != nil {
		t.Fatalf("record 1 failed: %v", err)
	}

	// 2. 模拟记录 2：谁填了文件
	err = store.Record(ctx, Entry{
		Timestamp:    now.Add(-8 * time.Minute),
		Type:         TypeFileUploaded,
		RecordID:     "rec_detail_2001",
		RelatedID:    "rec_tx_1001",
		Operator:     "ou_user_123",
		OperatorName: "张三",
		Action:       "上传发票附件: receipt.png",
		Details: map[string]any{
			"file_token": "Ku2JbSnzAo9lA9xGJi2c",
		},
	})
	if err != nil {
		t.Fatalf("record 2 failed: %v", err)
	}

	// 3. 模拟记录 3：什么时候发起了 OCR
	err = store.Record(ctx, Entry{
		Timestamp: now.Add(-7 * time.Minute),
		Type:      TypeOCRInitiated,
		RecordID:  "rec_detail_2001",
		Action:    "发起 Anyreceipt OCR 识别",
		Details: map[string]any{
			"provider": "anyreceipt",
		},
	})
	if err != nil {
		t.Fatalf("record 3 failed: %v", err)
	}

	// 4. 模拟记录 4：什么时候发起了审核
	err = store.Record(ctx, Entry{
		Timestamp: now.Add(-5 * time.Minute),
		Type:      TypeReviewSubmitted,
		RecordID:  "rec_detail_2001",
		Action:    "发起单据机审至 SealAI",
		Details: map[string]any{
			"provider":     "seal",
			"version_hash": "v-abc123",
		},
	})
	if err != nil {
		t.Fatalf("record 4 failed: %v", err)
	}

	// 测试 Timeline 查询 (通过 record_id 或 related_id)
	timeline, err := store.Timeline(ctx, "rec_detail_2001")
	if err != nil {
		t.Fatalf("Timeline failed: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("expected 3 entries for rec_detail_2001, got %d", len(timeline))
	}

	// 时间线应该从早到晚
	if timeline[0].Type != TypeFileUploaded || timeline[1].Type != TypeOCRInitiated || timeline[2].Type != TypeReviewSubmitted {
		t.Fatalf("timeline chronological order incorrect")
	}

	// 测试 FormatTimeline 输出
	rendered := FormatTimeline("rec_detail_2001", timeline)
	if !strings.Contains(rendered, "文件上传") || !strings.Contains(rendered, "发起OCR") || !strings.Contains(rendered, "发起审核") {
		t.Fatalf("rendered timeline missing key stages: %s", rendered)
	}

	// 测试 Query 过滤
	// 按 operator 查询
	userEntries, total, err := store.Query(ctx, QueryFilter{Operator: "张三"})
	if err != nil {
		t.Fatalf("query by operator failed: %v", err)
	}
	if total != 2 || len(userEntries) != 2 {
		t.Fatalf("expected 2 entries for 张三, got %d", total)
	}

	// 按 type 查询 (模糊匹配 ocr)
	ocrEntries, _, err := store.Query(ctx, QueryFilter{Type: "ocr"})
	if err != nil {
		t.Fatalf("query by type failed: %v", err)
	}
	if len(ocrEntries) != 1 || ocrEntries[0].Type != TypeOCRInitiated {
		t.Fatalf("expected 1 ocr entry, got %d", len(ocrEntries))
	}
}
