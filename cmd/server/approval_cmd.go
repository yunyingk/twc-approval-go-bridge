package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func newApprovalService(cfg config.Config) (*approval.Service, error) {
	source, err := newReviewSource(cfg)
	if err != nil {
		return nil, err
	}
	var reader approval.ReviewReader
	if cfg.Runtime.StateDir != "" {
		if files, err := state.OpenFiles(cfg.Runtime.StateDir); err == nil {
			reader = files
		}
	}
	client := approval.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	return approval.NewService(client, approval.ServiceOptions{
		ApprovalCode:     cfg.Feishu.Approval.ApprovalCode,
		DefaultApprover:  cfg.Feishu.Approval.DefaultApproverOpenID,
		DefaultInitiator: cfg.Feishu.Approval.DefaultInitiatorOpenID,
		Source:           source,
		ReviewReader:     reader,
	}), nil
}

// runApprovalPreview previews the Feishu approval instance request payload (Dry-Run, no network call to Feishu).
func runApprovalPreview(ctx context.Context, cfg config.Config, recordID string, output io.Writer) error {
	svc, err := newApprovalService(cfg)
	if err != nil {
		return err
	}
	req, err := svc.Preview(ctx, recordID)
	if err != nil {
		return fmt.Errorf("preview approval instance for %s: %w", recordID, err)
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(req)
}

// runApprovalPush pushes the approval instance to Feishu (one-way push, no callback/event listening).
func runApprovalPush(ctx context.Context, cfg config.Config, recordID string, output io.Writer) error {
	svc, err := newApprovalService(cfg)
	if err != nil {
		return err
	}
	resp, err := svc.Push(ctx, recordID)
	if err != nil {
		return fmt.Errorf("push approval instance for %s: %w", recordID, err)
	}

	fmt.Fprintf(output, "✅ 成功向飞书发起审批实例:\n")
	fmt.Fprintf(output, "  - 实例 Code: %s\n", resp.InstanceCode)
	if resp.InstanceURL != "" {
		fmt.Fprintf(output, "  - 审批链接: %s\n", resp.InstanceURL)
	}
	return nil
}
