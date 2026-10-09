package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/card"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "用法: go run ./cmd/tools/notify/main.go <transaction-record-id>\n")
		os.Exit(1)
	}

	recordID := os.Args[1]
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	svc, err := card.NewService(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化通知服务失败: %v\n", err)
		os.Exit(1)
	}

	result, err := svc.NotifyTransaction(context.Background(), recordID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "发送交易补票卡片失败: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}
