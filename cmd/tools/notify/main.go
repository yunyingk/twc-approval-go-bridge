package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/card"
)

func main() {
	force := flag.Bool("force", false, "强制发送通知（即使该流水已关联报销明细）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: go run ./cmd/tools/notify/main.go [-force] <transaction-record-id>\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	recordID := flag.Arg(0)
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

	result, err := svc.NotifyTransaction(context.Background(), recordID, card.NotifyOptions{Force: *force})
	if err != nil {
		fmt.Fprintf(os.Stderr, "发送交易补票卡片失败: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}
