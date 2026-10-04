package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func runReviewInspection(ctx context.Context, cfg config.Config, recordID string, live bool, output io.Writer) error {
	store, err := state.OpenFiles(cfg.StateDir)
	if err != nil {
		return err
	}
	var source app.Source
	if live {
		source, err = newReviewSource(cfg)
		if err != nil {
			return err
		}
	}
	inspector, err := app.NewInspector(source, store, len(cfg.ReviewResultFieldIDs) > 0, cfg.ReviewProvider)
	if err != nil {
		return err
	}
	if recordID == "all" {
		recordID = ""
	}
	report, err := inspector.Inspect(ctx, "feishu:"+cfg.ReceiptBaseToken+":"+cfg.ReceiptTableID, recordID, live)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(report)
}

func runReviewWriteback(ctx context.Context, cfg config.Config, documentID string, output io.Writer) error {
	service, err := newReviewDeliveryService(cfg)
	if err != nil {
		return err
	}
	status := "delivered"
	if err := service.RetryWriteback(ctx, documentID); err != nil {
		if !errors.Is(err, app.ErrStale) {
			return err
		}
		status = "superseded"
	}
	return json.NewEncoder(output).Encode(struct {
		DocumentID string `json:"document_id"`
		Status     string `json:"status"`
	}{documentID, status})
}
