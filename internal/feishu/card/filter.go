package card

import (
	"strconv"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

// FilterDecision represents the result of applying business rules to a transaction row.
type FilterDecision struct {
	ShouldNotify bool   `json:"should_notify"`
	Reason       string `json:"reason,omitempty"`
}

// EvaluateTransactionFilter applies dedicated, hardcoded Go business rules
// to filter out 0-amount, failed, withdrawn, or already linked transactions.
// Any modifications to transaction filtering logic should be maintained strictly within this file.
func EvaluateTransactionFilter(record map[string]any, fieldMap map[string]string, transBinding config.TableBinding) FilterDecision {
	getFieldValue := func(semantic string) any {
		if transBinding.Fields != nil {
			fieldID := transBinding.Fields[semantic]
			if fieldID != "" {
				fieldName := fieldMap[fieldID]
				if fieldName != "" {
					return record[fieldName]
				}
			}
		}
		return nil
	}

	// 1. Check detail relation (deduplication): already linked to personal reimbursement detail?
	detailRelRaw := getFieldValue("detail_relation")
	if detailRelRaw == nil {
		detailRelRaw = record["个人报销单号"]
	}
	if isAlreadyLinked(detailRelRaw) {
		return FilterDecision{
			ShouldNotify: false,
			Reason:       "该交易流水已关联个人报销明细，无需重复催报",
		}
	}

	// 2. Check transaction status: filter out failed, cancelled, reversed, or refunded
	txStatusRaw := getFieldValue("transaction_status")
	if txStatusRaw == nil {
		txStatusRaw = record["交易状态"]
	}
	txStatusStr := strings.ToLower(strings.TrimSpace(formatString(txStatusRaw, "")))
	if isIgnoredTransactionStatus(txStatusStr) {
		return FilterDecision{
			ShouldNotify: false,
			Reason:       "交易状态为无效/失败/撤销流水（" + txStatusStr + "），无需催报",
		}
	}

	// 3. Check claim status: filter out already claimed or no claim needed
	claimStatusRaw := getFieldValue("claim_status")
	if claimStatusRaw == nil {
		claimStatusRaw = record["报销状态"]
	}
	claimStatusStr := strings.ToLower(strings.TrimSpace(formatString(claimStatusRaw, "")))
	if isIgnoredClaimStatus(claimStatusStr) {
		return FilterDecision{
			ShouldNotify: false,
			Reason:       "报销状态已完结或无需报销（" + claimStatusStr + "），无需催报",
		}
	}

	// 4. Check amounts: filter out <= 0 amounts (e.g. $0 verification transactions)
	bookedAmtRaw := getFieldValue("booked_amount_cny")
	if bookedAmtRaw == nil {
		bookedAmtRaw = record["结算金额"]
	}
	origAmtRaw := getFieldValue("original_amount")
	if origAmtRaw == nil {
		origAmtRaw = record["交易金额"]
	}

	if isZeroOrNegativeAmount(bookedAmtRaw) && isZeroOrNegativeAmount(origAmtRaw) {
		return FilterDecision{
			ShouldNotify: false,
			Reason:       "交易金额为0或负数（预授权/核卡流水无需报销）",
		}
	}

	// 5. Check cardholder: must have a cardholder to notify
	cardholderRaw := getFieldValue("cardholder")
	if cardholderRaw == nil {
		cardholderRaw = record["持卡人"]
	}
	openID, _ := extractCardholderOpenID(cardholderRaw)
	if openID == "" {
		return FilterDecision{
			ShouldNotify: false,
			Reason:       "未识别到持卡人飞书有效账号",
		}
	}

	return FilterDecision{
		ShouldNotify: true,
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
		"已报销", "无需报销", "作废", "已归档", "已关闭", "不报销",
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
