package main

import (
	"context"
	"fmt"
	"io"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/health"
)

func runDoctor(ctx context.Context, cfg config.Config, jsonOutput bool, output io.Writer) error {
	checker := health.NewChecker(cfg)
	report := checker.CheckAll(ctx)
	if jsonOutput {
		return report.RenderJSON(output)
	}
	report.RenderText(output)
	if report.Failed > 0 {
		return fmt.Errorf("doctor check found %d blocking issues", report.Failed)
	}
	return nil
}
