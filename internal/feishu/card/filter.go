package card

import (
	"strconv"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

// DecisionCode classifies transaction filter decisions for stable metrics and reports.
type DecisionCode string

const (
	DecisionNotify           DecisionCode = "NOTIFY"
	DecisionIncompleteFields DecisionCode = "INCOMPLETE_FIELDS"
	DecisionNoCardholder     DecisionCode = "NO_CARDHOLDER"
	DecisionAlreadyLinked    DecisionCode = "ALREADY_LINKED"
	DecisionInvalidTxStatus  DecisionCode = "INVALID_TX_STATUS"
	DecisionClaimSettled     DecisionCode = "CLAIM_SETTLED"
	DecisionZeroOrNegative   DecisionCode = "ZERO_OR_NEGATIVE"
)

// FilterDecision represents the result of applying business rules to a transaction row.
type FilterDecision struct {
	ShouldNotify bool         `json:"should_notify"`
	Code         DecisionCode `json:"code"`
	Reason       string       `json:"reason,omitempty"`
}

// defaultFieldAliases defines standard Chinese column name fallbacks for transaction fields
// when the field ID binding is missing or during standalone unit testing.
var defaultFieldAliases = map[string][]string{
	"booked_amount_cny":  {"结算金额"},
	"booked_currency":    {"结算金额币种", "结算币种"},
	"transaction_id":     {"交易流水号"},
	"cardholder":         {"持卡人"},
	"transaction_time":   {"交易时间"},
	"detail_relation":    {"个人报销单号"},
	"transaction_status": {"交易状态"},
	"claim_status":       {"报销状态"},
	"original_amount":    {"交易金额"},
}

// EvaluateTransactionFilter applies dedicated, hardcoded Go business rules
// to filter out 0-amount, failed, withdrawn, or already linked transactions.
// Any modifications to transaction filtering logic should be maintained strictly within this file.
func EvaluateTransactionFilter(record map[string]any, fieldMap map[string]string, transBinding config.TableBinding) FilterDecision {
	getField := func(semantic string) any {
		if transBinding.Fields != nil {
			if fieldID := transBinding.Fields[semantic]; fieldID != "" {
				if fieldName := fieldMap[fieldID]; fieldName != "" {
					if val, ok := record[fieldName]; ok && val != nil {
						return val
					}
				}
			}
		}
		for _, alias := range defaultFieldAliases[semantic] {
			if val, ok := record[alias]; ok && val != nil {
				return val
			}
		}
		return nil
	}

	// 1. Mandatory 5 fields completeness check (Per business rule from Zhu Yitao):
	// Must guarantee that none of the following 5 fields are empty:
	// - 结算金额 (Booked Amount)
	// - 结算金额币种 (Booked Currency)
	// - 交易流水号 (Transaction ID)
	// - 持卡人 (Cardholder)
	// - 交易时间 (Transaction Time)
	// If any one of them is empty, do not send notification.
	bookedAmtRaw := getField("booked_amount_cny")
	if strings.TrimSpace(formatString(bookedAmtRaw, "")) == "" {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionIncompleteFields,
			Reason:       "缺少结算金额（未出账/在途交易），不予催报",
		}
	}

	bookedCurRaw := getField("booked_currency")
	if strings.TrimSpace(formatString(bookedCurRaw, "")) == "" {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionIncompleteFields,
			Reason:       "缺少结算金额币种，不予催报",
		}
	}

	txIDRaw := getField("transaction_id")
	if strings.TrimSpace(formatString(txIDRaw, "")) == "" {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionIncompleteFields,
			Reason:       "缺少交易流水号，不予催报",
		}
	}

	cardholderRaw := getField("cardholder")
	openID, _ := extractCardholderOpenID(cardholderRaw)
	if openID == "" {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionNoCardholder,
			Reason:       "未识别到持卡人飞书有效账号，不予催报",
		}
	}

	txTimeRaw := getField("transaction_time")
	if strings.TrimSpace(formatTime(txTimeRaw)) == "" {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionIncompleteFields,
			Reason:       "缺少交易时间，不予催报",
		}
	}

	// 2. Check detail relation (deduplication): already linked to personal reimbursement detail?
	detailRelRaw := getField("detail_relation")
	if isAlreadyLinked(detailRelRaw) {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionAlreadyLinked,
			Reason:       "该交易流水已关联个人报销明细，无需重复催报",
		}
	}

	// 3. Check transaction status: filter out failed, cancelled, reversed, or refunded
	txStatusRaw := getField("transaction_status")
	txStatusStr := strings.ToLower(strings.TrimSpace(formatString(txStatusRaw, "")))
	if isIgnoredTransactionStatus(txStatusStr) {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionInvalidTxStatus,
			Reason:       "交易状态为无效/失败/撤销流水（" + txStatusStr + "），无需催报",
		}
	}

	// 4. Check claim status: filter out already claimed or no claim needed (含 核销 语义)
	claimStatusRaw := getField("claim_status")
	claimStatusStr := strings.ToLower(strings.TrimSpace(formatString(claimStatusRaw, "")))
	if isIgnoredClaimStatus(claimStatusStr) {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionClaimSettled,
			Reason:       "报销状态已完结或无需核销（" + claimStatusStr + "），无需催报",
		}
	}

	// 5. Check amounts: filter out <= 0 amounts (e.g. $0 verification transactions)
	origAmtRaw := getField("original_amount")
	if isZeroOrNegativeAmount(bookedAmtRaw) && isZeroOrNegativeAmount(origAmtRaw) {
		return FilterDecision{
			ShouldNotify: false,
			Code:         DecisionZeroOrNegative,
			Reason:       "交易金额为0或负数（预授权/核卡流水无需报销）",
		}
	}

	return FilterDecision{
		ShouldNotify: true,
		Code:         DecisionNotify,
	}
}

func isIgnoredTransactionStatus(status string) bool {
	if status == "" {
		return false
	}
	ignoredKeywords := []string{
		"失败", "撤回", "撤销", "退款", "冲正", "未扣款", "已关闭", "作废",
		"declined", "reversed", "refunded", "cancelled", "canceled", "failed", "void",
	}
	for _, kw := range ignoredKeywords {
		if strings.Contains(status, kw) {
			return true
		}
	}
	return false
}

func isIgnoredClaimStatus(status string) bool {
	if status == "" {
		return false
	}
	ignoredKeywords := []string{
		"已报销", "无需报销", "无需核销", "已核销", "作废", "已归档", "已关闭", "不报销", "不核销",
	}
	for _, kw := range ignoredKeywords {
		if strings.Contains(status, kw) {
			return true
		}
	}
	return false
}

func isZeroOrNegativeAmount(raw any) bool {
	if raw == nil {
		return true
	}
	str := strings.TrimSpace(formatString(raw, ""))
	if str == "" {
		return true
	}
	// Parse float
	val, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return false // if cannot parse (e.g. formula or non-numeric), do not assume 0
	}
	return val <= 0.000001
}
