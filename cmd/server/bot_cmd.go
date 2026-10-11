package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/bot"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/notify"
	"github.com/yunyingk/twc-approval-go-bridge/internal/httpserver"
	"github.com/yunyingk/twc-approval-go-bridge/internal/version"
)

func runTestBotReply(ctx context.Context, cfg config.Config, args []string, w io.Writer) error {
	if !cfg.FeishuEnabled() {
		return errors.New("feishu credentials (app_id, app_secret) are required for bot simulation")
	}

	text := "管理页面"
	openID := ""
	if len(cfg.Notification.AdminOpenIDs) > 0 {
		openID = cfg.Notification.AdminOpenIDs[0]
	} else if len(cfg.Notification.OpenIDs) > 0 {
		openID = cfg.Notification.OpenIDs[0]
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-text":
			if i+1 < len(args) {
				text = strings.TrimSpace(args[i+1])
				i++
			}
		case "-open-id":
			if i+1 < len(args) {
				openID = strings.TrimSpace(args[i+1])
				i++
			}
		}
	}

	if openID == "" {
		return errors.New("未指定发送者 OpenID：请在配置中设置 [notification].open_ids 或在命令行指定 -open-id <id>")
	}

	tm := httpserver.NewTokenManager(cfg.Runtime.MagicTokenTTL)
	client := notify.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)

	statusFunc := func() httpserver.Status {
		return httpserver.Status{
			Version:         version.Version,
			HTTPAddr:        cfg.Runtime.HTTPAddr,
			Uptime:          "1m 0s",
			FeishuState:     "ready",
			ReceiptProvider: cfg.ReceiptProvider(),
			ReviewProvider:  cfg.ReviewProvider(),
		}
	}

	botHandler := bot.NewHandler(bot.Config{
		PublicBaseURL: cfg.Runtime.PublicBaseURL,
		AdminOpenIDs:  cfg.Notification.AdminOpenIDs,
		TokenTTL:      cfg.Runtime.MagicTokenTTL,
	}, client, tm, statusFunc, nil)

	fmt.Fprintf(w, "正在模拟飞书私信交互...\n")
	fmt.Fprintf(w, "发送者 OpenID: %s\n", openID)
	fmt.Fprintf(w, "消息内容: %q\n", text)
	fmt.Fprintf(w, "识别意图: %s\n", bot.MatchIntent(text))

	// Construct simulated event
	msgPayload := map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"event_id":   fmt.Sprintf("sim_%d", time.Now().UnixNano()),
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{
				"sender_id": map[string]any{
					"open_id": openID,
				},
				"sender_type": "user",
			},
			"message": map[string]any{
				"message_id":   fmt.Sprintf("om_sim_%d", time.Now().UnixNano()),
				"chat_type":    "p2p",
				"chat_id":      "oc_sim",
				"message_type": "text",
				"content":      fmt.Sprintf(`{"text":"%s"}`, text),
			},
		},
	}
	payloadBytes, err := json.Marshal(msgPayload)
	if err != nil {
		return fmt.Errorf("marshal simulation payload: %w", err)
	}

	simEv := events.Event{
		ID:      fmt.Sprintf("sim_%d", time.Now().UnixNano()),
		Type:    "im.message.receive_v1",
		Payload: payloadBytes,
	}

	if err := botHandler.Sink(ctx, simEv); err != nil {
		return fmt.Errorf("bot handler execution failed: %w", err)
	}

	fmt.Fprintf(w, "✅ 飞书机器人交互响应成功投递！\n")
	activeTokens := tm.ActiveTokens()
	if len(activeTokens) > 0 {
		fmt.Fprintf(w, "已生成临时授权凭证:\n")
		for _, tok := range activeTokens {
			fmt.Fprintf(w, "  • Token: %s\n", tok.Token)
			fmt.Fprintf(w, "  • 访问链接: %s/?token=%s\n", cfg.Runtime.PublicBaseURL, tok.Token)
			fmt.Fprintf(w, "  • 失效时间: %s (有效 2 小时)\n", tok.ExpiresAt.Format("2006-01-02 15:04:05"))
		}
	}
	return nil
}
