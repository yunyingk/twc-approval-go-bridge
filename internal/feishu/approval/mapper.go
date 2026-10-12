package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Template widget IDs for "海外易商卡消费确认单" (7B19DB2B-1495-4CD8-A723-881F3859EBCE).
const (
	WidgetTitle       = "widget17905653230" // input, required
	WidgetSubmitDate  = "widget17905653231" // date, required
	WidgetDetailList  = "widget17905653232" // fieldList, required

	// Child widgets under WidgetDetailList
	WidgetDetailID        = "widget17905653233"  // input: 明细ID
	WidgetTransactionID   = "widget17905653234"  // input: 关联流水号 (required)
	WidgetReimburser      = "widget17905653235"  // contact: 报销人
	WidgetExpenseDate     = "widget17905653236"  // date: 消费日期
	WidgetCategory        = "widget17905653237"  // radioV2: 费用类目
	WidgetProjectCode     = "widget17905653238"  // input: 关联项目编号
	WidgetExpenseReason   = "widget17905653239"  // textarea: 消费事由
	WidgetInvoiceKey      = "widget179056532310" // input: 发票唯一键
	WidgetDemoScenario    = "widget179056532311" // input: 演示场景
	WidgetGST             = "widget179056532312" // number: GST
	WidgetOriginalAmount  = "widget179056532313" // number: 流水金额_原币
	WidgetReviewStatus    = "widget179056532314" // radioV2: 审核状态
	WidgetAmountDiff      = "widget179056532315" // input: 金额差异
	WidgetCNYAmount       = "widget179056532316" // number: 流水金额_CNY
	WidgetLocked          = "widget179056532317" // radioV2: 锁定
	WidgetReviewComment   = "widget179056532318" // textarea: 审核意见
	WidgetCurrency        = "widget179056532319" // input: 币种
	WidgetInvoiceAmount   = "widget179056532320" // number: 发票金额
	WidgetParentRecord    = "widget179056532321" // input: 父记录
	WidgetRiskLevel       = "widget179056532322" // radioV2: 风险等级
)

// MapCategory converts business category text into template option value.
func MapCategory(raw string) string {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "仓储物流", "warehouse_logistics":
		return "warehouse_logistics"
	case "专业服务", "professional_service":
		return "professional_service"
	case "市场推广", "marketing":
		return "marketing"
	case "软件服务", "software":
		return "software"
	case "供应链服务", "supply_chain":
		return "supply_chain"
	default:
		return ""
	}
}

// MapReviewStatus converts decision/review status into template option value.
func MapReviewStatus(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "通过", "pass", "approve":
		return "pass"
	case "存疑", "review":
		return "review"
	case "驳回", "reject":
		return "reject"
	case "待审", "pending":
		return "pending"
	default:
		return ""
	}
}

// MapRiskLevel converts risk level into template option value.
func MapRiskLevel(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "高", "high":
		return "high"
	case "中", "medium":
		return "medium"
	case "低", "low":
		return "low"
	default:
		return ""
	}
}

// FormatUUID generates a deterministic UUID from input if UUID is not provided.
func FormatUUID(seed string) string {
	hash := sha256.Sum256([]byte(seed))
	hexStr := hex.EncodeToString(hash[:])
	// format as 8-4-4-4-12
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
}

// BuildCreateInstanceRequest maps domain ApprovalInput into CreateInstanceRequest.
func BuildCreateInstanceRequest(approvalCode string, input ApprovalInput) (CreateInstanceRequest, error) {
	if strings.TrimSpace(approvalCode) == "" {
		approvalCode = DefaultApprovalCode
	}
	if strings.TrimSpace(input.TransactionID) == "" {
		return CreateInstanceRequest{}, fmt.Errorf("transaction_id (关联流水号) is required by template")
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = fmt.Sprintf("海外易商卡消费确认 - %s", input.TransactionID)
	}

	subDate := input.SubmissionDate
	if subDate.IsZero() {
		subDate = time.Now()
	}
	dateStr := subDate.Format("2006-01-02")

	// Assemble Detail Row
	detailRow := []FormControl{
		{ID: WidgetDetailID, Type: "input", Value: input.RecordID},
		{ID: WidgetTransactionID, Type: "input", Value: input.TransactionID},
	}

	if strings.TrimSpace(input.CardholderOpenID) != "" {
		detailRow = append(detailRow, FormControl{
			ID:      WidgetReimburser,
			Type:    "contact",
			OpenIDs: []string{strings.TrimSpace(input.CardholderOpenID)},
		})
	}

	if strings.TrimSpace(input.ExpenseDate) != "" {
		expDate := input.ExpenseDate
		// If RFC3339 format, extract YYYY-MM-DD
		if t, err := time.Parse(time.RFC3339, expDate); err == nil {
			expDate = t.Format("2006-01-02")
		}
		detailRow = append(detailRow, FormControl{
			ID:    WidgetExpenseDate,
			Type:  "date",
			Value: expDate,
		})
	}

	if cat := MapCategory(input.ExpenseCategory); cat != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetCategory,
			Type:  "radioV2",
			Value: cat,
		})
	}

	if strings.TrimSpace(input.ProjectCode) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetProjectCode,
			Type:  "input",
			Value: strings.TrimSpace(input.ProjectCode),
		})
	}

	if strings.TrimSpace(input.ExpenseReason) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetExpenseReason,
			Type:  "textarea",
			Value: strings.TrimSpace(input.ExpenseReason),
		})
	}

	if strings.TrimSpace(input.InvoiceKeys) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetInvoiceKey,
			Type:  "input",
			Value: strings.TrimSpace(input.InvoiceKeys),
		})
	}

	if strings.TrimSpace(input.DemoScenario) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetDemoScenario,
			Type:  "input",
			Value: strings.TrimSpace(input.DemoScenario),
		})
	}

	if input.GST > 0 {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetGST,
			Type:  "number",
			Value: input.GST,
		})
	}

	if input.OriginalAmount > 0 {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetOriginalAmount,
			Type:  "number",
			Value: input.OriginalAmount,
		})
	}

	if status := MapReviewStatus(input.ReviewStatus); status != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetReviewStatus,
			Type:  "radioV2",
			Value: status,
		})
	}

	if strings.TrimSpace(input.AmountDifference) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetAmountDiff,
			Type:  "input",
			Value: strings.TrimSpace(input.AmountDifference),
		})
	}

	if input.CNYAmount > 0 {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetCNYAmount,
			Type:  "number",
			Value: input.CNYAmount,
		})
	}

	lockedVal := "false"
	if input.Locked {
		lockedVal = "true"
	}
	detailRow = append(detailRow, FormControl{
		ID:    WidgetLocked,
		Type:  "radioV2",
		Value: lockedVal,
	})

	if strings.TrimSpace(input.ReviewComment) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetReviewComment,
			Type:  "textarea",
			Value: strings.TrimSpace(input.ReviewComment),
		})
	}

	if strings.TrimSpace(input.Currency) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetCurrency,
			Type:  "input",
			Value: strings.TrimSpace(input.Currency),
		})
	}

	if input.InvoiceAmount > 0 {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetInvoiceAmount,
			Type:  "number",
			Value: input.InvoiceAmount,
		})
	}

	if strings.TrimSpace(input.ParentRecordID) != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetParentRecord,
			Type:  "input",
			Value: strings.TrimSpace(input.ParentRecordID),
		})
	}

	if risk := MapRiskLevel(input.RiskLevel); risk != "" {
		detailRow = append(detailRow, FormControl{
			ID:    WidgetRiskLevel,
			Type:  "radioV2",
			Value: risk,
		})
	}

	// Assemble Top-Level Form
	topForm := []FormControl{
		{ID: WidgetTitle, Type: "input", Value: title},
		{ID: WidgetSubmitDate, Type: "date", Value: dateStr},
		{ID: WidgetDetailList, Type: "fieldList", Value: [][]FormControl{detailRow}},
	}

	rawForm, err := json.Marshal(topForm)
	if err != nil {
		return CreateInstanceRequest{}, fmt.Errorf("marshal form array: %w", err)
	}

	uuid := strings.TrimSpace(input.UUID)
	if uuid == "" {
		uuid = FormatUUID(fmt.Sprintf("%s:%s:%f", input.RecordID, input.TransactionID, input.OriginalAmount))
	}

	req := CreateInstanceRequest{
		ApprovalCode: approvalCode,
		OpenID:       strings.TrimSpace(input.InitiatorOpenID),
		UUID:         uuid,
		Form:         string(rawForm),
	}

	if strings.TrimSpace(input.ApproverOpenID) != "" {
		req.NodeApproverList = []NodeApprover{
			{
				Key:   CustomNodeApproval,
				Value: []string{strings.TrimSpace(input.ApproverOpenID)},
			},
		}
	}

	return req, nil
}
