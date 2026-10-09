package provision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestDetectMemberType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"finance@example.com", "email"},
		{"ou_d9906271c1cc05281ad48757fb2bb71a", "openid"},
		{"oc_4789cd90463767a6d64d2a0ee9dc4f0d", "openchat"},
		{"user_12345", "userid"},
	}

	for _, tt := range tests {
		got := detectMemberType(tt.input)
		if got != tt.expected {
			t.Errorf("detectMemberType(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestProvisionBase_MockEndToEnd(t *testing.T) {
	createdTables := make(map[string]bool)
	createdFields := make(map[string]string)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// 1. Auth endpoint
		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"tenant_access_token": "test-tenant-token",
			})
			return
		}

		// 2. Create Base
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bitable/v1/apps") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"app": map[string]any{
						"app_token":        "base_mock_123456",
						"default_table_id": "tbl_default_init",
						"name":             "测试库",
						"url":              "https://feishu.cn/base/base_mock_123456",
					},
				},
			})
			return
		}

		// 3. AdvPerm Enable
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/advperm/enable") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{},
			})
			return
		}

		// 4. Create Tables
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tables") {
			var body struct {
				Table struct {
					Name   string          `json:"name"`
					Fields []TableFieldReq `json:"fields"`
				} `json:"table"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)

			tableID := "tbl_" + body.Table.Name
			createdTables[tableID] = true
			fieldIDs := make([]string, len(body.Table.Fields))
			for i, f := range body.Table.Fields {
				fID := "fld_" + f.FieldName
				fieldIDs[i] = fID
				createdFields[tableID+":"+f.FieldName] = fID
			}

			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"table_id":      tableID,
					"field_id_list": fieldIDs,
				},
			})
			return
		}

		// 5. Delete default table
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/tbl_default_init") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{},
			})
			return
		}

		// 6. Create Field (e.g. link field)
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/fields") {
			var body TableFieldReq
			_ = json.NewDecoder(r.Body).Decode(&body)
			fID := "fld_link_" + body.FieldName
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"field": map[string]any{
						"field_id":   fID,
						"field_name": body.FieldName,
					},
				},
			})
			return
		}

		// 7. Create Role
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/roles") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"role": map[string]any{
						"role_id":   "rol_mock_consumer",
						"role_name": "消费流水人员",
					},
				},
			})
			return
		}

		// 8. Add Collaborator
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/permissions/") && strings.HasSuffix(r.URL.Path, "/members") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{},
			})
			return
		}

		// 9. Transfer Owner
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/transfer_owner") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient("test_app_id", "test_app_secret")
	client.SetAPIBase(server.URL)
	svc := NewService(client)

	tmpDir := t.TempDir()
	outConfig := filepath.Join(tmpDir, "enterprise-mock.json")

	opts := ProvisionOptions{
		BaseName:          "自动化测试审核库",
		ProfileName:       "enterprise-test",
		OutputConfigFile:  outConfig,
		AdminUser:         "finance@example.com",
		TransferOwnerUser: "ou_owner123",
	}

	result, err := svc.ProvisionBase(context.Background(), opts)
	if err != nil {
		t.Fatalf("ProvisionBase failed: %v", err)
	}

	if result.BaseToken != "base_mock_123456" {
		t.Errorf("expected BaseToken base_mock_123456, got %s", result.BaseToken)
	}
	if !result.AdvPermActive {
		t.Errorf("expected AdvPermActive true")
	}
	if !strings.Contains(result.AdminAdded, "finance@example.com") {
		t.Errorf("expected AdminAdded to mention finance@example.com, got %s", result.AdminAdded)
	}
	if !strings.Contains(result.OwnerTransferred, "ou_owner123") {
		t.Errorf("expected OwnerTransferred to mention ou_owner123, got %s", result.OwnerTransferred)
	}

	// Validate the exported JSON file using config.LoadTablesFile
	if _, err := os.Stat(outConfig); err != nil {
		t.Fatalf("output config file was not created: %v", err)
	}

	loadedProfile, err := config.LoadTablesFile(outConfig)
	if err != nil {
		t.Fatalf("LoadTablesFile failed to validate exported config: %v", err)
	}

	if loadedProfile.Name != "enterprise-test" {
		t.Errorf("expected profile name enterprise-test, got %s", loadedProfile.Name)
	}
	if len(loadedProfile.Tables.InvoiceLedger.OrderedFields) != 20 {
		t.Errorf("expected 20 ordered fields in invoice_ledger, got %d", len(loadedProfile.Tables.InvoiceLedger.OrderedFields))
	}
	if loadedProfile.Tables.ReimbursementDetails.Fields["invoice_relation"] == "" {
		t.Errorf("expected details invoice_relation to be set")
	}
	if loadedProfile.Tables.Transactions.Fields["detail_relation"] == "" {
		t.Errorf("expected transactions detail_relation to be set")
	}
}

func TestProvisionBase_GracefulDowngradeOnFreeEdition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/auth/v3/tenant_access_token/internal") {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token"})
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bitable/v1/apps") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"app": map[string]any{
						"app_token": "base_free_edition",
						"name":      "免费版库",
						"url":       "https://feishu.cn/base/base_free_edition",
					},
				},
			})
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/advperm/enable") {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tables") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"table_id":      "tbl_mock",
					"field_id_list": []string{"fld_1", "fld_2", "fld_3", "fld_4", "fld_5", "fld_6", "fld_7", "fld_8", "fld_9", "fld_10", "fld_11", "fld_12", "fld_13", "fld_14", "fld_15", "fld_16", "fld_17", "fld_18", "fld_19", "fld_20"},
				},
			})
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/fields") {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"field": map[string]any{"field_id": "fld_link"}},
			})
			return
		}
		// Return 1254304 on role creation
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/roles") {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{
				"code": 1254304,
				"msg":  "Only Available For Business and Enterprise Editions",
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"code": 0})
	}))
	defer server.Close()

	client := NewClient("id", "sec")
	client.SetAPIBase(server.URL)
	svc := NewService(client)

	tmpDir := t.TempDir()
	outConfig := filepath.Join(tmpDir, "free.json")

	result, err := svc.ProvisionBase(context.Background(), ProvisionOptions{
		BaseName:         "免费企业库",
		OutputConfigFile: outConfig,
	})
	if err != nil {
		t.Fatalf("ProvisionBase should not fail on 1254304: %v", err)
	}

	if !strings.Contains(result.AdvPermNote, "基础版") {
		t.Errorf("expected AdvPermNote to mention 基础版 downgrade, got: %s", result.AdvPermNote)
	}
}

