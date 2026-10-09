package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/health"
)

func main() {
	jsonOutput := flag.Bool("json", false, "以 JSON 格式输出诊断报告")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: go run ./cmd/tools/doctor/main.go [-json]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置文件失败: %v\n", err)
		os.Exit(1)
	}

	checker := health.NewChecker(cfg)
	report := checker.CheckAll(context.Background())

	if *jsonOutput {
		if err := report.RenderJSON(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "渲染 JSON 报告失败: %v\n", err)
			os.Exit(1)
		}
	} else {
		report.RenderText(os.Stdout)
	}

	if report.Failed > 0 {
		os.Exit(1)
	}
}
