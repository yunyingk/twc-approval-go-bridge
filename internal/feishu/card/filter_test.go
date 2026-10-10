package card

import (
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestEvaluateTransactionFilter(t *testing.T) {
	binding := config.TableBinding{
		Fields: map[string]string{
			"transaction_id":     "fld_tx",
			"cardholder":         "fld_user",
			"booked_amount_cny":  "fld_cny",
			"booked_currency":    "fld_cny_cur",
			"transaction_time":   "fld_time",
			"original_amount":    "fld_orig",
			"transaction_status": "fld_status",
			"claim_status":       "fld_claim",
			"detail_relation":    "fld_rel",
		},
	}
	fieldMap := map[string]string{
		"fld_tx":      "交易流水号",
		"fld_user":    "持卡人",
		"fld_cny":     "结算金额",
		"fld_cny_cur": "结算金额币种",
		"fld_time":    "交易时间",
		"fld_orig":    "交易金额",
		"fld_status":  "交易状态",
		"fld_claim":   "报销状态",
		"fld_rel":     "个人报销单号",
	}

	validUser := []any{map[string]any{"id": "ou_test123", "name": "张三"}}

	t.Run("valid transaction should be notified", func(t *testing.T) {
		record := map[string]any{
			"交易流水号":   "TX-1001",
			"持卡人":     validUser,
			"结算金额":    "304.30",
			"结算金额币种":  "CNY",
			"交易时间":    "2026/10/06",
			"交易金额":    "42.00",
			"交易状态":    "交易成功",
			"报销状态":    "待报销",
		}
		decision := EvaluateTransactionFilter(record, fieldMap, binding)
		if !decision.ShouldNotify {
			t.Fatalf("expected should notify, got reason: %s", decision.Reason)
		}
	})

	t.Run("missing essential 5 fields should be skipped", func(t *testing.T) {
		base := func() map[string]any {
			return map[string]any{
				"交易流水号":   "TX-1001",
				"持卡人":     validUser,
				"结算金额":    "304.30",
				"结算金额币种":  "CNY",
				"交易时间":    "2026/10/06",
			}
		}

		// missing booked amount
		rec1 := base()
		delete(rec1, "结算金额")
		if d := EvaluateTransactionFilter(rec1, fieldMap, binding); d.ShouldNotify {
			t.Fatal("expected missing booked amount to be skipped")
		}

		// missing currency
		rec2 := base()
		delete(rec2, "结算金额币种")
		if d := EvaluateTransactionFilter(rec2, fieldMap, binding); d.ShouldNotify {
			t.Fatal("expected missing currency to be skipped")
		}

		// missing tx id
		rec3 := base()
		delete(rec3, "交易流水号")
		if d := EvaluateTransactionFilter(rec3, fieldMap, binding); d.ShouldNotify {
			t.Fatal("expected missing tx id to be skipped")
		}

		// missing cardholder
		rec4 := base()
		delete(rec4, "持卡人")
		if d := EvaluateTransactionFilter(rec4, fieldMap, binding); d.ShouldNotify {
			t.Fatal("expected missing cardholder to be skipped")
		}

		// missing tx time
		rec5 := base()
		delete(rec5, "交易时间")
		if d := EvaluateTransactionFilter(rec5, fieldMap, binding); d.ShouldNotify {
			t.Fatal("expected missing tx time to be skipped")
		}
	})

	t.Run("zero amount should be skipped", func(t *testing.T) {
		record := map[string]any{
			"交易流水号":   "TX-1002",
			"持卡人":     validUser,
			"结算金额":    "0",
			"结算金额币种":  "CNY",
			"交易时间":    "2026/10/06",
			"交易金额":    "0.00",
			"交易状态":    "交易成功",
		}
		decision := EvaluateTransactionFilter(record, fieldMap, binding)
		if decision.ShouldNotify {
			t.Fatal("expected zero amount to be skipped")
		}
		if decision.Reason == "" {
			t.Fatal("expected reason for skip")
		}
	})

	t.Run("failed transaction status should be skipped", func(t *testing.T) {
		statuses := []string{"交易失败", "已撤销", "撤回", "已退款", "冲正", "declined", "failed"}
		for _, st := range statuses {
			record := map[string]any{
				"交易流水号":   "TX-1003",
				"持卡人":     validUser,
				"结算金额":    "100.00",
				"结算金额币种":  "CNY",
				"交易时间":    "2026/10/06",
				"交易状态":    st,
			}
			decision := EvaluateTransactionFilter(record, fieldMap, binding)
			if decision.ShouldNotify {
				t.Fatalf("expected status %q to be skipped", st)
			}
		}
	})

	t.Run("claim status already claimed should be skipped (including 核销 keywords)", func(t *testing.T) {
		claimStatuses := []string{"已报销", "无需报销", "无需核销", "已核销", "作废"}
		for _, cs := range claimStatuses {
			record := map[string]any{
				"交易流水号":   "TX-1004",
				"持卡人":     validUser,
				"结算金额":    "100.00",
				"结算金额币种":  "CNY",
				"交易时间":    "2026/10/06",
				"报销状态":    cs,
			}
			decision := EvaluateTransactionFilter(record, fieldMap, binding)
			if decision.ShouldNotify {
				t.Fatalf("expected claim status %q to be skipped", cs)
			}
		}
	})

	t.Run("already linked transaction should be skipped", func(t *testing.T) {
		record := map[string]any{
			"交易流水号":   "TX-1005",
			"持卡人":     validUser,
			"结算金额":    "100.00",
			"结算金额币种":  "CNY",
			"交易时间":    "2026/10/06",
			"个人报销单号":  []any{map[string]any{"record_ids": []any{"rec_detail_1"}}},
		}
		decision := EvaluateTransactionFilter(record, fieldMap, binding)
		if decision.ShouldNotify {
			t.Fatal("expected linked transaction to be skipped")
		}
	})
}
