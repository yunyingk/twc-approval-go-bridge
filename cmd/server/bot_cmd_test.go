package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestRunTestBotReply_Validation(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer

	// 1. Missing Feishu credentials
	cfgNoFeishu := config.Config{}
	err := runTestBotReply(ctx, cfgNoFeishu, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "feishu credentials") {
		t.Fatalf("expected error for missing credentials, got %v", err)
	}

	// 2. Missing OpenID target
	cfgNoTarget := config.Config{
		Feishu: config.FeishuSettings{
			AppCredentials: config.AppCredentials{
				AppID:     "cli_test",
				AppSecret: "secret_test",
			},
		},
	}
	buf.Reset()
	err = runTestBotReply(ctx, cfgNoTarget, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "未指定发送者 OpenID") {
		t.Fatalf("expected error for missing open id, got %v", err)
	}
}
