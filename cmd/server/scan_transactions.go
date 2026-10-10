package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/card"
)

func runScanTransactions(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("scan-transactions", flag.ContinueOnError)
	fs.SetOutput(output)

	send := fs.Bool("send", false, "真实向持卡人发送卡片与创建待办（默认 false 为纯只读分析）")
	limit := fs.Int("limit", 0, "最大发送条数（0 表示发送全部符合条件的候选，建议测试时设为 1）")
	user := fs.String("user", "", "仅针对特定持卡人姓名或 open_id 进行筛选催报")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	svc, err := card.NewService(cfg)
	if err != nil {
		return err
	}
	scanner := card.NewScanner(svc, logger)

	modeText := "只读分析模式 (Dry-Run / 不发送消息)"
	if *send {
		modeText = "【真实发送模式】"
		if *limit > 0 {
			modeText += fmt.Sprintf(" (限制最大发送 %d 条)", *limit)
		}
	}

	fmt.Fprintf(output, "🔍 正在拉取交易流水表并进行内存高速初筛 [%s]...\n", modeText)

	report, err := scanner.Inspect(ctx, card.ScanOptions{
		Send:  *send,
		Limit: *limit,
		User:  *user,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(output, "\n======================================================================\n")
	fmt.Fprintf(output, "📊 交易流水初筛与巡检报告 (耗时: %v)\n", report.Duration)
	fmt.Fprintf(output, "======================================================================\n")
	fmt.Fprintf(output, "流水总记录数:        %d 条\n", report.TotalRows)
	fmt.Fprintf(output, "  - 已关联报销明细:  %d 条 (无需催报)\n", report.SkippedLinked)
	if report.SkippedIncomplete > 0 {
		fmt.Fprintf(output, "  - 缺少必填要素(未入账): %d 条 (已过滤)\n", report.SkippedIncomplete)
	}
	fmt.Fprintf(output, "  - 0元/负数验证流水: %d 条 (已过滤)\n", report.SkippedZeroAmount)
	fmt.Fprintf(output, "  - 失败/撤回/撤销:   %d 条 (已过滤)\n", report.SkippedStatus)
	fmt.Fprintf(output, "  - 已报销/无需核销:  %d 条 (已过滤)\n", report.SkippedClaim)
	if report.SkippedNoUser > 0 {
		fmt.Fprintf(output, "  - 未识别到持卡人:  %d 条 (跳过)\n", report.SkippedNoUser)
	}
	fmt.Fprintf(output, "----------------------------------------------------------------------\n")
	fmt.Fprintf(output, "🎯 符合催交的有效候选: %d 条\n", len(report.Candidates))

	if len(report.Candidates) > 0 {
		fmt.Fprintf(output, "\n📋 待催交流水明细:\n")
		maxShow := 10
		if len(report.Candidates) < maxShow {
			maxShow = len(report.Candidates)
		}
		for i := 0; i < maxShow; i++ {
			c := report.Candidates[i]
			fmt.Fprintf(output, "  %2d. [%s] 流水号: %s | 商户: %s | 金额: %s | 持卡人: %s\n",
				i+1, c.RecordID, c.TransactionID, c.Merchant, c.BookedAmount, c.Cardholder)
		}
		if len(report.Candidates) > maxShow {
			fmt.Fprintf(output, "      ... 还有 %d 条未展开\n", len(report.Candidates)-maxShow)
		}
	}

	fmt.Fprintf(output, "======================================================================\n")

	if !*send {
		fmt.Fprintf(output, "\n💡 当前处于【安全只读模式】，未向任何用户发送飞书卡片或创建待办。\n")
		if len(report.Candidates) > 0 {
			fmt.Fprintf(output, "👉 若要真实批量发送催交卡片，请执行:\n")
			fmt.Fprintf(output, "   ./bin/twc-approval-go-bridge scan-transactions -send -limit 1\n")
		}
	} else {
		fmt.Fprintf(output, "\n🚀 实际发送完成: 已成功向 %d 位持卡人投递补票卡片与待办！\n", report.NotifiedCount)
	}

	return nil
}
