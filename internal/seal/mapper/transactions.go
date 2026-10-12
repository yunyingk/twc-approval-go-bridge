package mapper

import (
	"encoding/json"
	"fmt"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
)

// TransactionFields preserves the common snapshot as TEXT evidence. It does
// not turn payment amounts into invoice claims, allocations or exchange rates.
func TransactionFields(evidence *core.TransactionEvidence) ([]seal.DocumentField, error) {
	if evidence == nil {
		return nil, nil
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("encode transaction evidence: %w", err)
	}
	fields := []seal.DocumentField{{Key: "bridge_transaction_evidence", Label: "关联支付流水及资料完整性", Type: "TEXT", Value: string(raw), Comments: "来源：交易流水表快照"}}
	for index, transaction := range evidence.Transactions {
		values := []struct{ key, label, value, comments string }{
			{"record_id", "流水来源记录 ID", transaction.RecordID, "来源：交易流水表.记录ID"},
			{"transaction_id", "交易流水号", transaction.TransactionID, "来源：交易流水表.合思交易流水号"},
			{"merchant", "商户名称", transaction.Merchant, "来源：交易流水表.商户名称"},
			{"occurred_at", "消费日期", transaction.OccurredAt, "来源：交易流水表.交易时间"},
			{"original_amount", "报销原币金额", transaction.OriginalAmount, "来源：交易流水表.交易金额(原币)"},
			{"original_currency", "交易原币种", transaction.OriginalCurrency, "来源：交易流水表.交易原币种"},
			{"booked_amount_cny", "CNY结算金额", transaction.BookedAmountCNY, "来源：交易流水表.记账金额(CNY)"},
			{"country", "交易国家地区", transaction.Country, "来源：交易流水表.交易国家地区"},
			{"transaction_type", "交易类型", transaction.TransactionType, "来源：交易流水表.交易类型"},
			{"status", "流水状态", transaction.Status, "来源：交易流水表.交易状态"},
		}
		for _, value := range values {
			if value.value != "" {
				fields = append(fields, seal.DocumentField{
					Key:      fmt.Sprintf("bridge_transaction_%02d_%s", index+1, value.key),
					Label:    value.label,
					Type:     "TEXT",
					Value:    value.value,
					Comments: value.comments,
				})
			}
		}
	}
	return fields, nil
}
