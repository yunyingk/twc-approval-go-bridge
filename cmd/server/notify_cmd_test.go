package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestRunTestNotifyValidation(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer

	// 1. Feishu disabled should fail
	cfgNoFeishu := config.Config{}
	err := runTestNotify(ctx, cfgNoFeishu, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "feishu credentials") {
		t.Fatalf("expected error about missing feishu credentials, got: %v", err)
	}

	// 2. Feishu enabled but no targets configured and no flags
	cfgNoTargets := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "cli_test",
				AppSecret: "sec_test",
			},
		},
	}
	buf.Reset()
	err = runTestNotify(ctx, cfgNoTargets, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "未指定任何接收目标") {
		t.Fatalf("expected error about missing targets, got: %v", err)
	}
}
