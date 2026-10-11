package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func runListFailedReceipts(ctx context.Context, cfg config.Config, jsonOutput bool, output io.Writer) error {
	store, err := state.NewFiles(cfg.Runtime.StateDir)
	if err != nil {
		return err
	}
	failed, err := store.FailedRecognition(ctx, "")
	if err != nil {
		return err
	}

	if jsonOutput {
		return json.NewEncoder(output).Encode(failed)
	}

	if len(failed) == 0 {
		fmt.Fprintln(output, "暂无死信识别任务 (所有识别任务均已成功处理或正在正常重试中)")
		return nil
	}

	w := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RECORD_ID\tFILE_TOKEN\tATTEMPTS\tLAST_ATTEMPT\tLAST_ERROR")
	for _, t := range failed {
		lastTime := "-"
		if !t.LastAttemptAt.IsZero() {
			lastTime = t.LastAttemptAt.Local().Format("2006-01-02 15:04:05")
		}
		errMsg := t.LastError
		if len(errMsg) > 60 {
			errMsg = errMsg[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", t.Result.RecordID, t.Result.FileToken, t.Attempts, lastTime, errMsg)
	}
	return w.Flush()
}

func runRetryReceipt(ctx context.Context, cfg config.Config, recordIDOrKey string, output io.Writer) error {
	store, err := state.NewFiles(cfg.Runtime.StateDir)
	if err != nil {
		return err
	}
	count, err := store.RetryRecognition(ctx, recordIDOrKey)
	if err != nil {
		return err
	}
	if count == 0 {
		fmt.Fprintf(output, "未找到目标单据/任务或该任务未处于失败状态: %s\n", recordIDOrKey)
		return nil
	}
	fmt.Fprintf(output, "已成功重置 %d 个识别任务 (ID: %s)，将在下一个轮询周期自动重新发起识别\n", count, recordIDOrKey)
	return nil
}
