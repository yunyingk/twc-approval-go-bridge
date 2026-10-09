package main

import (
	"bytes"
	"context"
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
