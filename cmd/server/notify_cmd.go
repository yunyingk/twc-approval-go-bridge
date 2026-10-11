package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/notify"
)

func runTestNotify(ctx context.Context, cfg config.Config, args []string, w io.Writer) error {
	if !cfg.FeishuEnabled() {
		return errors.New("feishu credentials (app_id, app_secret) are required for notifications")
	}

	chatIDs := cfg.Notification.ChatIDs
	openIDs := cfg.Notification.OpenIDs

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-chat":
			if i+1 < len(args) {
				val := strings.TrimSpace(args[i+1])
				if val != "" {
					chatIDs = []string{val}
					openIDs = nil
				}
				i++
			}
		case "-open-id":
			if i+1 < len(args) {
				val := strings.TrimSpace(args[i+1])
				if val != "" {
					openIDs = []string{val}
					chatIDs = nil
				}
				i++
			}
		}
	}

	if len(chatIDs) == 0 && len(openIDs) == 0 {
		return errors.New("未指定任何接收目标：请在 configs/config.toml 中配置 [notification] 的 chat_ids / open_ids，或在命令行使用 -chat <id> / -open-id <id>")
	}

	client := notify.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	svc := notify.NewService(notify.Config{
		Enabled: true,
		ChatIDs: chatIDs,
		OpenIDs: openIDs,
	}, client, slog.Default())

	fmt.Fprintf(w, "正在向飞书机器人发送连通性测试卡片...\n")
	if len(chatIDs) > 0 {
		fmt.Fprintf(w, "目标群聊 (%d): %s\n", len(chatIDs), strings.Join(chatIDs, ", "))
	}
	if len(openIDs) > 0 {
		fmt.Fprintf(w, "目标用户 (%d): %s\n", len(openIDs), strings.Join(openIDs, ", "))
	}
	fmt.Fprintln(w)

	results, err := svc.SendTestCard(ctx, notify.TestNotice{
		TriggeredBy: "CLI 运维诊断 (test-notify)",
		Timestamp:   time.Now(),
		Note:        "这是海外小票识别与 AI 审核桥接服务的通道测试消息，确认能否正常接收告警卡片。",
	})

	for _, res := range results {
		if res.MessageID != "" {
			fmt.Fprintf(w, "  ✅ [%s] %s -> 发送成功 (Message ID: %s)\n", res.TargetType, res.TargetID, res.MessageID)
		} else {
			fmt.Fprintf(w, "  ❌ [%s] %s -> 发送失败: %s\n", res.TargetType, res.TargetID, res.Error)
		}
	}

	if err != nil {
		return fmt.Errorf("部分目标投递失败: %w", err)
	}

	fmt.Fprintln(w, "\n全部测试卡片投递成功！")
	return nil
}
