package audit

import (
	"time"
)

// EventType identifies the lifecycle milestone of a transaction or reimbursement record.
type EventType string

const (
	// 1. 什么时候推送了谁
	TypeNotifyCardSent    EventType = "notify_card_sent"    // 向持卡人推送补票提醒卡片成功
	TypeNotifyCardSkipped EventType = "notify_card_skipped" // 评估后跳过推送 (已补票/无须补票)

	// 2. 谁填了文件
	TypeFileUploaded EventType = "file_uploaded" // 员工/操作人上传了发票或附件文件

	// 3. 什么时候发起了 OCR
	TypeOCRInitiated EventType = "ocr_initiated" // 发起小票 OCR 识别任务
	TypeOCRCompleted EventType = "ocr_completed" // 小票 OCR 识别成功并提取关键要素
	TypeOCRFailed    EventType = "ocr_failed"    // 小票 OCR 识别失败

	// 4. 什么时候发起了审核
	TypeReviewSubmitted EventType = "review_submitted" // 向审核提供方发起机审请求
	TypeReviewCompleted EventType = "review_completed" // 收到审核结论 (通过/复核/驳回)
	TypeReviewWriteback EventType = "review_writeback" // 审核结论成功写回飞书明细表
)

// Entry captures structured business audit metadata for operations and compliance.
type Entry struct {
	ID           string         `json:"id"`                     // 唯一事件ID (例如 evt-1728551234-abcd)
	Timestamp    time.Time      `json:"timestamp"`              // 发生时间
	Type         EventType      `json:"type"`                   // 事件类型
	RecordID     string         `json:"record_id"`              // 核心记录ID (明细表ID 或 交易流水ID)
	RelatedID    string         `json:"related_id,omitempty"`   // 关联记录ID (如关联的流水ID或明细ID)
	Operator     string         `json:"operator,omitempty"`     // 接收人或操作人 (open_id / 邮箱 / 电话)
	OperatorName string         `json:"operator_name,omitempty"`// 姓名或花名
	Action       string         `json:"action"`                 // 动作摘要说明
	Details      map[string]any `json:"details,omitempty"`     // 动态结构化元数据
}

// QueryFilter defines filtering criteria for querying audit logs.
type QueryFilter struct {
	RecordID string    `json:"record_id,omitempty"` // 匹配 RecordID 或 RelatedID
	Type     string    `json:"type,omitempty"`      // 事件类型精确或前缀匹配 (如 ocr, review, notify, file)
	Operator string    `json:"operator,omitempty"`  // 匹配 Operator 或 OperatorName
	Since    time.Time `json:"since,omitempty"`     // 起始时间
	Until    time.Time `json:"until,omitempty"`     // 结束时间
	Limit    int       `json:"limit,omitempty"`     // 返回最大条数 (默认 50)
	Offset   int       `json:"offset,omitempty"`    // 偏移量
}
