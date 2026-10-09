package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestRunInitBitable_RequiresFeishuCredentials(t *testing.T) {
	cfg := config.Config{} // Empty credentials
	var out bytes.Buffer

	err := runInitBitable(context.Background(), cfg, []string{}, &out)
	if err == nil {
		t.Fatalf("expected error when feishu credentials are missing")
	}
	if !strings.Contains(err.Error(), "requires Feishu app credentials") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunInitBitable_HelpFlag(t *testing.T) {
	cfg := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "cli_test",
				AppSecret: "sec_test",
			},
		},
	}
	var out bytes.Buffer

	err := runInitBitable(context.Background(), cfg, []string{"-h"}, &out)
	if err != nil {
		t.Fatalf("expected nil on -h, got %v", err)
	}
	if !strings.Contains(out.String(), "Usage of init-bitable:") {
		t.Errorf("expected usage output, got %s", out.String())
	}
}

func TestRunAddAdmin_RequiresArgs(t *testing.T) {
	cfg := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "cli_test",
				AppSecret: "sec_test",
			},
		},
	}
	var out bytes.Buffer

	err := runAddAdmin(context.Background(), cfg, "", "", &out)
	if err == nil {
		t.Fatalf("expected error when args are empty")
	}
}

func TestRunTransferOwner_RequiresArgs(t *testing.T) {
	cfg := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "cli_test",
				AppSecret: "sec_test",
			},
		},
	}
	var out bytes.Buffer

	err := runTransferOwner(context.Background(), cfg, "", "", &out)
	if err == nil {
		t.Fatalf("expected error when args are empty")
	}
}

func TestUpdateConfigFileTablesPath(t *testing.T) {
	tmpFile := t.TempDir() + "/config.toml"
	initialContent := `# Config file
tables_file = "configs/tables/old.json"

[feishu]
app_id = "test"
`
	if err := os.WriteFile(tmpFile, []byte(initialContent), 0600); err != nil {
		t.Fatal(err)
	}

	newTablesPath := "configs/tables/enterprise-prod.json"
	if err := updateConfigFileTablesPath(tmpFile, newTablesPath); err != nil {
		t.Fatalf("updateConfigFileTablesPath failed: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `tables_file = "configs/tables/enterprise-prod.json"`) {
		t.Errorf("expected updated tables_file, got:\n%s", content)
	}
	if strings.Contains(content, "old.json") {
		t.Errorf("expected old.json to be replaced")
	}
}

