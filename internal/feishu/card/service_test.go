package card

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		Transactions: config.TransactionConfig{
			UserWhitelist: []config.WhitelistUser{
				{OpenID: "ou_holder_test", Name: "张三"},
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

	// formatTime formats with the process timezone; pin UTC+8 so the
	// expected calendar date holds on UTC build hosts too.
	previousLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*3600)
	defer func() { time.Local = previousLocal }()
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

func TestServiceNotifyTransaction_Option1_PrefillAndTask(t *testing.T) {
	var createdDetailRecord bool
	var createdTask bool
	var sentCardMessage bool

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
						{"field_id": "fld_orig_amt", "field_name": "交易金额"},
						{"field_id": "fld_orig_cur", "field_name": "交易金额币种"},
						{"field_id": "fld_detail_rel", "field_name": "关联流水号"},
						{"field_id": "fld_emp", "field_name": "报销人"},
						{"field_id": "fld_reason", "field_name": "消费事由"},
					},
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/records/rec_tx_test") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"record": map[string]any{
						"fields": map[string]any{
							"交易流水号": "TX20261009002",
							"商户名称":   "OPENAI *CHATGPT",
							"交易时间":   float64(1790524800000),
							"结算金额":   "146.52",
							"结算金额币种": "CNY",
							"交易金额":   "20.00",
							"交易金额币种": "USD",
							"持卡人": []any{
								map[string]any{"id": "ou_xzh", "name": "谢子豪"},
							},
						},
					},
				},
			})
			return
		}

		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tables/tbl_detail_id/records") {
			createdDetailRecord = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"record": map[string]any{
						"record_id": "rec_detail_test_999",
					},
				},
			})
			return
		}

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/task/v2/tasks") {
			createdTask = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"task": map[string]any{
						"guid": "mock_task_guid_888",
					},
				},
			})
			return
		}

		if strings.HasSuffix(r.URL.Path, "/im/v1/messages") {
			sentCardMessage = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{
					"message_id": "om_test_card_999",
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
					BaseToken: "app_base_token",
					TableID:   "tbl_detail_id",
					Fields: map[string]string{
						"transaction_relation": "fld_detail_rel",
						"employee":             "fld_emp",
						"expense_reason":       "fld_reason",
					},
				},
			},
		},
		Transactions: config.TransactionConfig{
			UserWhitelist: []config.WhitelistUser{
				{OpenID: "ou_xzh", Name: "谢子豪"},
			},
		},
	}

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.client.baseURL = server.URL
	if svc.taskClient != nil {
		svc.taskClient.SetBaseURLForTest(server.URL)
	}

	res, err := svc.NotifyTransaction(context.Background(), "rec_tx_test")
	if err != nil {
		t.Fatalf("NotifyTransaction failed: %v", err)
	}

	if !createdDetailRecord {
		t.Errorf("expected detail record to be created in Bitable")
	}
	if !createdTask {
		t.Errorf("expected Feishu task to be created")
	}
	if !sentCardMessage {
		t.Errorf("expected message card to be sent")
	}
	if res.DetailRecordID != "rec_detail_test_999" {
		t.Errorf("unexpected DetailRecordID: %s", res.DetailRecordID)
	}
	if res.TaskGUID != "mock_task_guid_888" {
		t.Errorf("unexpected TaskGUID: %s", res.TaskGUID)
	}
	if !strings.Contains(res.RecordURL, "record=rec_detail_test_999") {
		t.Errorf("expected direct record url, got: %s", res.RecordURL)
	}
}

func TestBatchCreatePrefillDetails(t *testing.T) {
	batchCreated := false
	var receivedRecords []map[string]any

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
						{"field_id": "fld_detail_rel", "field_name": "关联流水号"},
						{"field_id": "fld_emp", "field_name": "报销人"},
						{"field_id": "fld_reason", "field_name": "消费事由"},
						{"field_id": "fld_comment", "field_name": "审核意见"},
					},
				},
			})
			return
		}

		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/records/batch_create") {
			batchCreated = true
			var body struct {
				Records []map[string]any `json:"records"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			receivedRecords = body.Records

			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"records": []map[string]any{
						{"record_id": "rec_batch_001"},
						{"record_id": "rec_batch_002"},
					},
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
				ReimbursementDetails: config.TableBinding{
					BaseToken: "app_base_token",
					TableID:   "tbl_detail_id",
					Fields: map[string]string{
						"transaction_relation": "fld_detail_rel",
						"employee":             "fld_emp",
						"expense_reason":       "fld_reason",
						"human_review_comment": "fld_comment",
					},
				},
			},
		},
	}

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.client.baseURL = server.URL

	items := []SilentDetailItem{
		{
			TxRecordID:    "rec_tx_1",
			OpenID:        "ou_user_1",
			Merchant:      "MERCHANT ONE",
			ExpenseReason: "[历史流水/待补发票] MERCHANT ONE",
			ReviewComment: "历史流水批量建单归档（待补发票）",
		},
		{
			TxRecordID:    "rec_tx_2",
			OpenID:        "ou_user_2",
			Merchant:      "MERCHANT TWO",
			ExpenseReason: "[历史流水/待补发票] MERCHANT TWO",
			ReviewComment: "历史流水批量建单归档（待补发票）",
		},
	}

	createdIDs, err := svc.BatchCreatePrefillDetails(context.Background(), items)
	if err != nil {
		t.Fatalf("BatchCreatePrefillDetails failed: %v", err)
	}

	if !batchCreated {
		t.Fatal("expected batch_create to be called")
	}
	if len(createdIDs) != 2 || createdIDs[0] != "rec_batch_001" || createdIDs[1] != "rec_batch_002" {
		t.Fatalf("unexpected createdIDs: %v", createdIDs)
	}
	if len(receivedRecords) != 2 {
		t.Fatalf("expected 2 received records, got %d", len(receivedRecords))
	}
}

func TestService_IsUserWhitelisted(t *testing.T) {
	// 1. Strategy A: Empty whitelist blocks everyone
	svcEmpty := &Service{
		cfg: config.Config{
			Transactions: config.TransactionConfig{
				UserWhitelist: nil,
			},
		},
	}
	allowed, reason := svcEmpty.IsUserWhitelisted("ou_test123", "张三")
	if allowed {
		t.Errorf("expected blocked when whitelist is nil/empty, got allowed")
	}
	if !strings.Contains(reason, "未配置员工白名单") {
		t.Errorf("unexpected reason: %s", reason)
	}

	// 2. Exact match with OpenID and Name
	svc := &Service{
		cfg: config.Config{
			Transactions: config.TransactionConfig{
				UserWhitelist: []config.WhitelistUser{
					{OpenID: "ou_123", Name: "张三"},
					{OpenID: "ou_456", Name: ""}, // name optional
				},
			},
		},
	}

	// Hit with matching name
	allowed, _ = svc.IsUserWhitelisted("ou_123", "张三")
	if !allowed {
		t.Errorf("expected allowed for ou_123 with matching name")
	}

	// Hit with case-insensitivity and whitespace
	allowed, _ = svc.IsUserWhitelisted(" OU_123 ", " 张三 ")
	if !allowed {
		t.Errorf("expected allowed with whitespace/case-insensitivity")
	}

	// Hit with name mismatch (logs warn, but still allows by open_id as the golden standard)
	allowed, _ = svc.IsUserWhitelisted("ou_123", "李四")
	if !allowed {
		t.Errorf("expected allowed by open_id even when name differs")
	}

	// Hit when name was left empty in whitelist
	allowed, _ = svc.IsUserWhitelisted("ou_456", "王五")
	if !allowed {
		t.Errorf("expected allowed for ou_456 when whitelist name is empty")
	}

	// Miss: user not in whitelist
	allowed, reason = svc.IsUserWhitelisted("ou_789", "赵六")
	if allowed {
		t.Errorf("expected blocked for ou_789 not in whitelist")
	}
	if !strings.Contains(reason, "未在员工白名单中") {
		t.Errorf("unexpected reason: %s", reason)
	}

	// 3. Wildcard allows all
	svcWildcard := &Service{
		cfg: config.Config{
			Transactions: config.TransactionConfig{
				UserWhitelist: []config.WhitelistUser{
					{OpenID: "*"},
				},
			},
		},
	}
	allowed, reason = svcWildcard.IsUserWhitelisted("ou_random_user", "某路人")
	if !allowed {
		t.Errorf("expected allowed under wildcard whitelist")
	}
	if !strings.Contains(reason, "通配符") {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestServiceNotifyTransaction_WhitelistBlocked(t *testing.T) {
	messageSent := false
	taskCreated := false
	detailCreated := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "mock-token",
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
					},
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/records/rec_blocked_tx") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok",
				"data": map[string]any{
					"record": map[string]any{
						"fields": map[string]any{
							"交易流水号": "TX_BLOCKED_001",
							"商户名称":   "BLOCKED STORE",
							"交易时间":   float64(1790524800000),
							"结算金额":   "99.00",
							"结算金额币种": "CNY",
							"持卡人": []any{
								map[string]any{"id": "ou_blocked_user", "name": "未授权员工"},
							},
						},
					},
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/tables/tbl_detail_id/records") {
			detailCreated = true
			return
		}
		if strings.HasSuffix(r.URL.Path, "/task/v2/tasks") {
			taskCreated = true
			return
		}
		if strings.HasSuffix(r.URL.Path, "/im/v1/messages") {
			messageSent = true
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	// Whitelist only allows ou_other_user
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
					},
				},
				ReimbursementDetails: config.TableBinding{
					BaseToken: "app_base_token",
					TableID:   "tbl_detail_id",
				},
			},
		},
		Transactions: config.TransactionConfig{
			UserWhitelist: []config.WhitelistUser{
				{OpenID: "ou_allowed_someone_else", Name: "已授权测试员"},
			},
		},
	}

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.client.baseURL = server.URL
	if svc.taskClient != nil {
		svc.taskClient.SetBaseURLForTest(server.URL)
	}

	res, err := svc.NotifyTransaction(context.Background(), "rec_blocked_tx")
	if err != nil {
		t.Fatalf("NotifyTransaction returned error: %v", err)
	}

	if res.Status != "skipped_not_in_whitelist" {
		t.Errorf("expected status skipped_not_in_whitelist, got %s", res.Status)
	}
	if !strings.Contains(res.Reason, "未在员工白名单中") {
		t.Errorf("expected reason to mention 未在员工白名单中, got: %s", res.Reason)
	}
	if detailCreated {
		t.Errorf("detail record should NOT be created for non-whitelisted user")
	}
	if taskCreated {
		t.Errorf("task should NOT be created for non-whitelisted user")
	}
	if messageSent {
		t.Errorf("message card should NOT be sent for non-whitelisted user")
	}
}

func TestScanner_WhitelistFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "mock-token",
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

		if strings.Contains(r.URL.Path, "/tables/tbl_trans_id/records") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"record_id": "rec_1",
							"fields": map[string]any{
								"交易流水号":   "TX_001",
								"商户名称":     "MERCHANT A",
								"交易时间":     float64(1790524800000),
								"结算金额":     "100.00",
								"结算金额币种":   "CNY",
								"持卡人": []any{
									map[string]any{"id": "ou_whitelisted_1", "name": "测试员A"},
								},
							},
						},
						{
							"record_id": "rec_2",
							"fields": map[string]any{
								"交易流水号":   "TX_002",
								"商户名称":     "MERCHANT B",
								"交易时间":     float64(1790524800000),
								"结算金额":     "200.00",
								"结算金额币种":   "CNY",
								"持卡人": []any{
									map[string]any{"id": "ou_not_in_whitelist", "name": "普通员工B"},
								},
							},
						},
					},
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	// Whitelist only includes ou_whitelisted_1
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
						"booked_currency":   "fld_booked_cur",
					},
				},
				ReimbursementDetails: config.TableBinding{
					FormShareToken: "form_token_test",
				},
			},
		},
		Transactions: config.TransactionConfig{
			UserWhitelist: []config.WhitelistUser{
				{OpenID: "ou_whitelisted_1", Name: "测试员A"},
			},
		},
	}

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	svc.client.baseURL = server.URL

	scanner := NewScanner(svc, nil)
	report, err := scanner.Inspect(context.Background(), ScanOptions{Send: false})
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	if report.TotalRows != 2 {
		t.Errorf("expected 2 total rows, got %d", report.TotalRows)
	}
	if report.SkippedNotWhitelisted != 1 {
		t.Errorf("expected 1 SkippedNotWhitelisted, got %d", report.SkippedNotWhitelisted)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("expected exactly 1 candidate (the whitelisted one), got %d", len(report.Candidates))
	}
	if report.Candidates[0].OpenID != "ou_whitelisted_1" {
		t.Errorf("unexpected candidate: %s", report.Candidates[0].OpenID)
	}
}

