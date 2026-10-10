package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/audit"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func runAuditTimeline(ctx context.Context, cfg config.Config, recordID string, args []string, w io.Writer) error {
	jsonOutput := false
	for _, arg := range args {
		if arg == "-json" || arg == "--json" {
			jsonOutput = true
		}
	}

	entries, err := audit.Default().Timeline(ctx, recordID)
	if err != nil {
		return fmt.Errorf("query audit timeline: %w", err)
	}

	if jsonOutput {
		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}

	_, err = fmt.Fprint(w, audit.FormatTimeline(recordID, entries))
	return err
}

func runAuditQuery(ctx context.Context, cfg config.Config, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("audit-query", flag.ContinueOnError)
	fs.SetOutput(w)

	var (
		recordID   string
		eventType  string
		user       string
		sinceStr   string
		untilStr   string
		limit      int
		jsonOutput bool
	)

	fs.StringVar(&recordID, "id", "", "按单据记录ID过滤 (明细表ID 或 交易流水ID)")
	fs.StringVar(&recordID, "record-id", "", "按单据记录ID过滤")
	fs.StringVar(&eventType, "type", "", "按事件类型过滤 (notify, file, ocr, review)")
	fs.StringVar(&user, "user", "", "按操作人/接收人过滤 (open_id 或 姓名)")
	fs.StringVar(&sinceStr, "since", "", "起始时间 (如 2026-10-10 或 RFC3339)")
	fs.StringVar(&untilStr, "until", "", "结束时间 (如 2026-10-11 或 RFC3339)")
	fs.IntVar(&limit, "limit", 50, "返回最大条数 (默认 50)")
	fs.BoolVar(&jsonOutput, "json", false, "输出 JSON 格式")

	if err := fs.Parse(args); err != nil {
		return err
	}

	filter := audit.QueryFilter{
		RecordID: recordID,
		Type:     eventType,
		Operator: user,
		Limit:    limit,
	}

	if sinceStr != "" {
		if t, err := parseTime(sinceStr); err == nil {
			filter.Since = t
		} else {
			return fmt.Errorf("invalid -since format: %w", err)
		}
	}
	if untilStr != "" {
		if t, err := parseTime(untilStr); err == nil {
			filter.Until = t
		} else {
			return fmt.Errorf("invalid -until format: %w", err)
		}
	}

	entries, total, err := audit.Default().Query(ctx, filter)
	if err != nil {
		return fmt.Errorf("query audit: %w", err)
	}

	if jsonOutput {
		out := map[string]any{
			"total":   total,
			"count":   len(entries),
			"entries": entries,
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}

	if len(entries) == 0 {
		fmt.Fprintf(w, "未找到匹配的业务审计日志 (筛选条件: id=%s, type=%s, user=%s)\n", recordID, eventType, user)
		return nil
	}

	fmt.Fprintf(w, "共找到 %d 条业务审计日志 (显示前 %d 条):\n", total, len(entries))
	fmt.Fprintf(w, "--------------------------------------------------------------------------------\n")
	for _, e := range entries {
		fmt.Fprintln(w, audit.FormatEntry(e))
	}
	fmt.Fprintf(w, "--------------------------------------------------------------------------------\n")
	return nil
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range formats {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time: %s", s)
}
