package card

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
)

func TestEventSink_DebounceAndNotify(t *testing.T) {
	var notifiedCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "mock-tenant-token",
			})
			return
		}

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
						{"field_id": "fld_booked_cur", "field_name": "结算金额币种"},
						{"field_id": "fld_orig_amt", "field_name": "交易金额"},
						{"field_id": "fld_orig_cur", "field_name": "交易金额币种"},
					},
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/records/rec_event_tx_1") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"record": map[string]any{
						"fields": map[string]any{
							"交易流水号":   "TX_EV_001",
							"商户名称":     "REALTIME MERCHANT",
							"交易时间":     float64(1790524800000),
							"结算金额":     "128.00",
							"结算金额币种":   "CNY",
							"交易金额":     "18.00",
							"交易金额币种":   "USD",
							"持卡人": []any{
								map[string]any{"id": "ou_event_user", "name": "测试员"},
							},
						},
					},
				},
			})
			return
		}

		if strings.HasSuffix(r.URL.Path, "/im/v1/messages") {
			atomic.AddInt32(&notifiedCount, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{
					"message_id": "om_realtime_msg",
				},
			})
			return
		}

		http.NotFound(w, r)
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
					BaseToken: "app_base_tx",
					TableID:   "tbl_tx_id",
					Fields: map[string]string{
						"transaction_id":    "fld_tx_id",
						"cardholder":        "fld_cardholder",
						"merchant":          "fld_merchant",
						"transaction_time":  "fld_time",
						"booked_amount_cny": "fld_booked_amt",
						"booked_currency":   "fld_booked_cur",
						"original_amount":   "fld_orig_amt",
						"original_currency": "fld_orig_cur",
					},
				},
				ReimbursementDetails: config.TableBinding{
					FormShareToken: "form_token_test",
				},
			},
		},
	}

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.client.baseURL = server.URL

	sink := NewEventSink(svc, EventSinkOptions{Debounce: 50 * time.Millisecond})
	defer sink.Close()

	payloadMatch := []byte(`{
		"event": {
			"file_token": "app_base_tx",
			"table_id": "tbl_tx_id",
			"action_list": [
				{"action": "record_added", "record_id": "rec_event_tx_1"}
			]
		}
	}`)

	// Send twice quickly to test debounce merging
	if err := sink.Sink(context.Background(), events.Event{Payload: payloadMatch}); err != nil {
		t.Fatalf("sink error: %v", err)
	}
	if err := sink.Sink(context.Background(), events.Event{Payload: payloadMatch}); err != nil {
		t.Fatalf("sink error: %v", err)
	}

	// Wait for debounce timer to fire
	time.Sleep(150 * time.Millisecond)

	if count := atomic.LoadInt32(&notifiedCount); count != 1 {
		t.Fatalf("expected exactly 1 notification after debounced merge, got %d", count)
	}
}

func TestEventSink_IgnoreOtherTables(t *testing.T) {
	cfg := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "app_test",
				AppSecret: "secret_test",
			},
		},
		Business: &config.BusinessProfile{
			Tables: config.BusinessTables{
				Transactions: config.TableBinding{
					BaseToken: "app_base_tx",
					TableID:   "tbl_tx_id",
				},
			},
		},
	}

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	sink := NewEventSink(svc, EventSinkOptions{Debounce: 50 * time.Millisecond})
	defer sink.Close()

	payloadOther := []byte(`{
		"event": {
			"file_token": "other_base",
			"table_id": "other_table",
			"action_list": [
				{"action": "record_added", "record_id": "rec_other_1"}
			]
		}
	}`)

	if err := sink.Sink(context.Background(), events.Event{Payload: payloadOther}); err != nil {
		t.Fatalf("sink error: %v", err)
	}

	sink.mu.Lock()
	timerCount := len(sink.timers)
	sink.mu.Unlock()

	if timerCount != 0 {
		t.Fatalf("expected 0 timers for other table event, got %d", timerCount)
	}
}
