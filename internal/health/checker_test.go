package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestChecker_FullPass(t *testing.T) {
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

		// 2. Bot info
		if strings.HasSuffix(r.URL.Path, "/bot/v3/info") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"bot": map[string]any{
					"app_name": "TestBot",
					"activate": true,
				},
			})
			return
		}

		// 3. Task list
		if strings.HasSuffix(r.URL.Path, "/task/v2/tasks") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
			})
			return
		}

		// 4. Task comments
		if strings.HasSuffix(r.URL.Path, "/task/v2/comments") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
			})
			return
		}

		// 5. Table fields
		if strings.HasSuffix(r.URL.Path, "/fields") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"items": []map[string]any{
						{"field_id": "fld_tx_id", "field_name": "交易流水号", "type": 1},
						{"field_id": "fld_detail_id", "field_name": "明细ID", "type": 1},
						{"field_id": "fld_ledger_id", "field_name": "台账ID", "type": 1},
					},
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	tempDir := t.TempDir()

	cfg := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "cli_test_123",
				AppSecret: "sec_test_456",
			},
		},
		Runtime: config.RuntimeConfig{
			StateDir: tempDir,
		},
		Business: &config.BusinessProfile{
			Name: "test-profile",
			Recognition: config.RecognitionSettings{
				Provider: "anyreceipt",
			},
			Review: config.ReviewSettings{
				Provider: "seal",
			},
			Tables: config.BusinessTables{
				Transactions: config.TableBinding{
					BaseToken: "app_base_1",
					TableID:   "tbl_tx_1",
					Fields: map[string]string{
						"tx_id": "fld_tx_id",
					},
				},
				ReimbursementDetails: config.TableBinding{
					BaseToken: "app_base_1",
					TableID:   "tbl_detail_1",
					Fields: map[string]string{
						"detail_id": "fld_detail_id",
					},
				},
				InvoiceLedger: config.TableBinding{
					BaseToken: "app_base_1",
					TableID:   "tbl_ledger_1",
					Fields: map[string]string{
						"ledger_id": "fld_ledger_id",
					},
				},
			},
		},
		Anyreceipt: config.AnyreceiptSettings{
			APIKey: "mock-anyreceipt-token",
		},
		Seal: config.SealSettings{
			WebhookID: "mock-webhook-id",
		},
	}

	checker := NewChecker(cfg)
	checker.SetBaseURLForTest(server.URL)

	report := checker.CheckAll(context.Background())
	if report.Failed > 0 {
		t.Fatalf("expected 0 failed items, got %d: %+v", report.Failed, report.Items)
	}
	if report.OverallStatus != "100% HEALTHY (全部探测项通过，可放心启动服务！)" {
		t.Fatalf("unexpected overall status: %s", report.OverallStatus)
	}

	var buf strings.Builder
	report.RenderText(&buf)
	if !strings.Contains(buf.String(), "100% HEALTHY") {
		t.Errorf("rendered text missing healthy summary: %s", buf.String())
	}
}

func TestChecker_PermissionFail(t *testing.T) {
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

		if strings.HasSuffix(r.URL.Path, "/bot/v3/info") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"bot":  map[string]any{"app_name": "TestBot", "activate": true},
			})
			return
		}

		// Permission missing on task comments
		if strings.HasSuffix(r.URL.Path, "/task/v2/comments") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 99991672,
				"msg":  "Access denied. task:comment:write is required",
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer server.Close()

	cfg := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "cli_test",
				AppSecret: "sec_test",
			},
		},
		Runtime: config.RuntimeConfig{
			StateDir: t.TempDir(),
		},
	}

	checker := NewChecker(cfg)
	checker.SetBaseURLForTest(server.URL)

	report := checker.CheckAll(context.Background())
	if report.Failed == 0 {
		t.Fatalf("expected failed item for permission denied")
	}

	var foundCommentFail bool
	for _, item := range report.Items {
		if strings.Contains(item.Name, "task:comment:write") && item.Status == StatusFail {
			foundCommentFail = true
			if !strings.Contains(item.Remedy, "task:comment:write") {
				t.Errorf("expected remedy to mention permission, got: %s", item.Remedy)
			}
		}
	}
	if !foundCommentFail {
		t.Errorf("expected task:comment:write failure item in report")
	}
}
