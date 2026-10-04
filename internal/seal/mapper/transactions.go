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
	fields := []seal.DocumentField{{Key: "bridge_transaction_evidence", Label: "关联支付流水及资料完整性", Type: "TEXT", Value: string(raw)}}
	for index, transaction := range evidence.Transactions {
		values := []struct{ key, label, value string }{
			{"record_id", "流水来源记录 ID", transaction.RecordID},
			{"transaction_id", "交易流水号", transaction.TransactionID},
			{"merchant", "交易商户", transaction.Merchant},
			{"occurred_at", "交易时间（UTC）", transaction.OccurredAt},
			{"original_amount", "交易金额（原币）", transaction.OriginalAmount},
			{"original_currency", "交易原币种", transaction.OriginalCurrency},
			{"booked_amount_cny", "记账金额（CNY）", transaction.BookedAmountCNY},
			{"country", "交易国家地区", transaction.Country},
			{"transaction_type", "交易类型", transaction.TransactionType},
			{"status", "流水状态", transaction.Status},
		}
		for _, value := range values {
			if value.value != "" {
				fields = append(fields, seal.DocumentField{Key: fmt.Sprintf("bridge_transaction_%02d_%s", index+1, value.key), Label: value.label, Type: "TEXT", Value: value.value})
			}
		}
	}
	return fields, nil
}
