//go:build !no_anthropic

// Package model recognizes image receipts through an Anthropic-compatible Messages API.
package model

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/yunyingk/twc-approval-go-bridge/internal/anthropic"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receiptcompat"
)

type Client struct {
	api   *anthropic.Client
	model string
}

var _ invoice.Recognizer = (*Client)(nil)

func New(apiKey, baseURL, model string) (*Client, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("receipt model name is required")
	}
	api, err := anthropic.NewWithBaseURL(apiKey, baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{api: api, model: model}, nil
}

func (c *Client) Recognize(ctx context.Context, attachment invoice.Attachment) (invoice.Recognition, error) {
	if c == nil || c.api == nil {
		return invoice.Recognition{}, fmt.Errorf("receipt model client is not initialized")
	}
	switch attachment.ContentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return invoice.Recognition{}, fmt.Errorf("receipt model does not support %q; PDF/DOCX conversion is a later module", attachment.ContentType)
	}
	if len(attachment.Data) == 0 {
		return invoice.Recognition{}, fmt.Errorf("receipt image data is empty")
	}
	const prompt = `Extract the visible receipt facts without inventing values. Return only one JSON object with "facts" and "summary". Facts use the keys title, country, receipt_type, number, issue_date, currency, seller, buyer, business_category, tax_rate, total, tax, pretax. All values are strings; absent facts are empty strings. Preserve source amount and date text. If this file contains more than one distinct receipt, return {"multiple_receipts":true} instead of silently merging them.`
	msg, err := c.api.CreateMessage(ctx, sdk.MessageNewParams{
		Model: sdk.Model(c.model), MaxTokens: 2048,
		Messages: []sdk.MessageParam{sdk.NewUserMessage(
			sdk.NewTextBlock(prompt),
			sdk.NewImageBlockBase64(attachment.ContentType, base64.StdEncoding.EncodeToString(attachment.Data)),
		)},
	})
	if err != nil {
		return invoice.Recognition{}, fmt.Errorf("recognize receipt with model: %w", err)
	}
	raw, err := anthropic.JSONMessage(msg)
	if err != nil {
		return invoice.Recognition{}, err
	}
	result, err := receiptcompat.Decode(raw)
	if err != nil {
		return invoice.Recognition{}, err
	}
	result.Origin.Provider, result.Origin.Model = "model", c.model
	return result, nil
}
