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

		// 5. Contact scopes
		if strings.HasSuffix(r.URL.Path, "/contact/v3/scopes") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"user_ids": []string{"ou_test_1"},
				},
			})
			return
		}

		// 6. Drive permission members (collaborator)
		if strings.Contains(r.URL.Path, "/drive/v1/permissions/") && strings.HasSuffix(r.URL.Path, "/members") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"member_id":   "cli_test_123",
							"member_type": "appid",
							"perm":        "full_access",
						},
					},
				},
			})
			return
		}

		// 7. Bitable roles
		if strings.Contains(r.URL.Path, "/bitable/v1/apps/") && strings.HasSuffix(r.URL.Path, "/roles") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"role_id":   "rol_employee",
							"role_name": "员工角色",
							"table_roles": []map[string]any{
								{"table_id": "tbl_tx_1", "table_perm": 0},
								{"table_id": "tbl_detail_1", "table_perm": 2},
							},
						},
					},
				},
			})
			return
		}

		// 8. Bitable app meta
		if strings.HasPrefix(r.URL.Path, "/bitable/v1/apps/") && !strings.Contains(r.URL.Path, "/tables/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"app": map[string]any{
						"is_advanced": true,
					},
				},
			})
			return
		}

		// 9. Table fields
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
