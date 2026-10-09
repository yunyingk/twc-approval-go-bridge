package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/card"
)

// runNotifyTransaction is the thin CLI adapter for the card.Service.
// It exercises the pure domain service without duplicating any Feishu or Bitable logic.
func runNotifyTransaction(ctx context.Context, cfg config.Config, transactionRecordID string, output io.Writer) error {
	svc, err := card.NewService(cfg)
	if err != nil {
		return err
	}
	result, err := svc.NotifyTransaction(ctx, transactionRecordID)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
