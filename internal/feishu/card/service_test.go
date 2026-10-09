package card

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestServiceNotifyTransaction_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// 1. Auth token
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "mock-tenant-token",
			})
			return
		}

		// 2. Fields schema
		if strings.HasSuffix(r.URL.Path, "/fields") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"items": []map[string]any{
						{"field_id": "fld_tx_id", "field_name": "交易流水号"},
						{"field_id": "fld_cardholder", "field_name": "持卡人"},
						{"field_id": "fld_merchant", "field_name": "商户名称"},
						{"field_id": "fld_time", "field_name": "交易时间"},
						{"field_id": "fld_booked_amt", "field_name": "结算金额"},
						{"field_id": "fld_orig_amt", "field_name": "交易金额"},
						{"field_id": "fld_orig_cur", "field_name": "交易金额币种"},
					},
				},
			})
			return
		}

		// 3. Get record
		if strings.Contains(r.URL.Path, "/records/rec_test_123") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"record": map[string]any{
						"fields": map[string]any{
							"交易流水号": "TX20261009001",
							"商户名称":   "TEST MERCHANT INC",
							"交易时间":   float64(1790524800000),
							"结算金额":   "100.00",
							"结算金额币种": "CNY",
							"交易金额":   "15.00",
							"交易金额币种": "USD",
							"持卡人": []any{
								map[string]any{"id": "ou_holder_test", "name": "张三"},
							},
						},
					},
				},
			})
			return
		}

		// 4. Send message
		if strings.HasSuffix(r.URL.Path, "/im/v1/messages") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["receive_id"] != "ou_holder_test" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{
					"message_id": "om_test_msg_888",
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "app_test",
				AppSecret: "secret_test",
			},
			Host: "https://test.feishu.cn",
		},
		Business: &config.BusinessProfile{
			Tables: config.BusinessTables{
				Transactions: config.TableBinding{
					BaseToken: "app_base_token",
					TableID:   "tbl_trans_id",
					Fields: map[string]string{
						"transaction_id":    "fld_tx_id",
						"cardholder":        "fld_cardholder",
						"merchant":          "fld_merchant",
						"transaction_time":  "fld_time",
						"booked_amount_cny": "fld_booked_amt",
						"original_amount":   "fld_orig_amt",
						"original_currency": "fld_orig_cur",
					},
				},
				ReimbursementDetails: config.TableBinding{
					FormShareToken: "form_token_abc",
				},
			},
		},
	}

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.client.baseURL = server.URL
	svc.client.httpClient = server.Client()

	res, err := svc.NotifyTransaction(context.Background(), "rec_test_123")
	if err != nil {
		t.Fatalf("NotifyTransaction failed: %v", err)
	}

	if res.MessageID != "om_test_msg_888" {
		t.Errorf("expected message_id om_test_msg_888, got %s", res.MessageID)
	}
	if res.Recipient != "张三" || res.OpenID != "ou_holder_test" {
		t.Errorf("unexpected recipient: %s (%s)", res.Recipient, res.OpenID)
	}
	if res.TransactionID != "TX20261009001" {
		t.Errorf("unexpected txID: %s", res.TransactionID)
	}
	if !strings.Contains(res.FormURL, "prefill_关联交易流水号=rec_test_123") {
		t.Errorf("expected prefill query in form URL, got %s", res.FormURL)
	}
}

func TestFormatHelpers(t *testing.T) {
	// formatString
	if got := formatString("hello"); got != "hello" {
		t.Errorf("formatString single = %s", got)
	}
	if got := formatString("", nil, "fallback"); got != "fallback" {
		t.Errorf("formatString fallback = %s", got)
	}

	// formatTime
	if got := formatTime(float64(1790524800000)); got != "2026/09/28" {
		t.Errorf("formatTime millis = %s", got)
	}
	if got := formatTime("2026-09-28"); got != "2026-09-28" {
		t.Errorf("formatTime str = %s", got)
	}

	// extractCardholderOpenID
	raw := []any{
		map[string]any{"id": "ou_123", "name": "李四"},
	}
	id, name := extractCardholderOpenID(raw)
	if id != "ou_123" || name != "李四" {
		t.Errorf("extractCardholderOpenID = %s, %s", id, name)
	}
}
