package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/audit"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestRunAuditTimelineAndQuery(t *testing.T) {
	tempDir := t.TempDir()
	store, err := audit.InitFileStore(tempDir)
	if err != nil {
		t.Fatalf("InitFileStore failed: %v", err)
	}

	ctx := context.Background()
	now := time.Now()

	// 1. 模拟事件：什么时候推送了谁
	_ = store.Record(ctx, audit.Entry{
		Timestamp:    now.Add(-30 * time.Minute),
		Type:         audit.TypeNotifyCardSent,
		RecordID:     "rec_tx_9001",
		RelatedID:    "rec_detail_8001",
		Operator:     "ou_emp_001",
		OperatorName: "王五",
		Action:       "向持卡人王五推送交易补票卡片",
		Details: map[string]any{
			"merchant": "DJI Store",
			"amount":   "5999.00 CNY",
		},
	})

	// 2. 模拟事件：谁填了文件
	_ = store.Record(ctx, audit.Entry{
		Timestamp:    now.Add(-20 * time.Minute),
		Type:         audit.TypeFileUploaded,
		RecordID:     "rec_detail_8001",
		RelatedID:    "rec_tx_9001",
		Operator:     "ou_emp_001",
		OperatorName: "王五",
		Action:       "上传发票附件: dji_invoice.pdf",
		Details: map[string]any{
			"file_token": "Ku2JbSnzAo9lA9xGJi2c",
		},
	})

	// 3. 模拟事件：什么时候发起了 OCR
	_ = store.Record(ctx, audit.Entry{
		Timestamp: now.Add(-19 * time.Minute),
		Type:      audit.TypeOCRInitiated,
		RecordID:  "rec_detail_8001",
		Action:    "发起 Anyreceipt OCR 识别",
		Details: map[string]any{
			"provider": "anyreceipt",
		},
	})
	_ = store.Record(ctx, audit.Entry{
		Timestamp: now.Add(-18 * time.Minute),
		Type:      audit.TypeOCRCompleted,
		RecordID:  "rec_detail_8001",
		Action:    "小票 OCR 识别完成 (金额: CNY 5999.00, 商户: DJI Store)",
		Details: map[string]any{
			"amount":   "5999.00",
			"currency": "CNY",
			"trace_id": "OCR-999888",
		},
	})

	// 4. 模拟事件：什么时候发起了审核
	_ = store.Record(ctx, audit.Entry{
		Timestamp: now.Add(-10 * time.Minute),
		Type:      audit.TypeReviewSubmitted,
		RecordID:  "rec_detail_8001",
		Action:    "向机审引擎发起送审请求",
		Details: map[string]any{
			"provider": "seal",
		},
	})
	_ = store.Record(ctx, audit.Entry{
		Timestamp: now.Add(-8 * time.Minute),
		Type:      audit.TypeReviewCompleted,
		RecordID:  "rec_detail_8001",
		Action:    "单据机审完成 (结论: approved)",
		Details: map[string]any{
			"decision": "approved",
		},
	})
	_ = store.Record(ctx, audit.Entry{
		Timestamp: now.Add(-7 * time.Minute),
		Type:      audit.TypeReviewWriteback,
		RecordID:  "rec_detail_8001",
		Action:    "机审结论与审核留痕已成功回写至多维表格明细表",
	})

	cfg := config.Config{}

	// 测试 1: 查询单据时间线 (文本模式)
	var bufTimeline bytes.Buffer
	err = runAuditTimeline(ctx, cfg, "rec_detail_8001", nil, &bufTimeline)
	if err != nil {
		t.Fatalf("runAuditTimeline failed: %v", err)
	}
	outTimeline := bufTimeline.String()
	if !strings.Contains(outTimeline, "推送卡片") ||
		!strings.Contains(outTimeline, "文件上传") ||
		!strings.Contains(outTimeline, "发起OCR") ||
		!strings.Contains(outTimeline, "发起审核") ||
		!strings.Contains(outTimeline, "结果回写") {
		t.Fatalf("timeline missing expected milestones:\n%s", outTimeline)
	}

	// 测试 2: 查询单据时间线 (-json 模式)
	var bufJSON bytes.Buffer
	err = runAuditTimeline(ctx, cfg, "rec_detail_8001", []string{"-json"}, &bufJSON)
	if err != nil {
		t.Fatalf("runAuditTimeline -json failed: %v", err)
	}
	if !strings.Contains(bufJSON.String(), `"type": "notify_card_sent"`) || !strings.Contains(bufJSON.String(), `"type": "review_writeback"`) {
		t.Fatalf("timeline json output invalid:\n%s", bufJSON.String())
	}

	// 测试 3: audit-query 按人员过滤
	var bufUser bytes.Buffer
	err = runAuditQuery(ctx, cfg, []string{"-user", "王五"}, &bufUser)
	if err != nil {
		t.Fatalf("runAuditQuery -user failed: %v", err)
	}
	if !strings.Contains(bufUser.String(), "王五") {
		t.Fatalf("expected audit query to contain 王五: %s", bufUser.String())
	}

	// 测试 4: audit-query 按类型过滤 (ocr)
	var bufType bytes.Buffer
	err = runAuditQuery(ctx, cfg, []string{"-type", "ocr"}, &bufType)
	if err != nil {
		t.Fatalf("runAuditQuery -type failed: %v", err)
	}
	if !strings.Contains(bufType.String(), "OCR") {
		t.Fatalf("expected audit query to contain OCR: %s", bufType.String())
	}
}
