// Package model recognizes image receipts through an Anthropic-compatible Messages API.
package model

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/yunyingk/twc-approval-go-bridge/internal/anthropic"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
)

type Client struct {
	api   *anthropic.Client
	model string
}

var _ receipt.Recognizer = (*Client)(nil)

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

func (c *Client) Recognize(ctx context.Context, attachment receipt.Attachment) (receipt.Recognition, error) {
	if c == nil || c.api == nil {
		return receipt.Recognition{}, fmt.Errorf("receipt model client is not initialized")
	}
	switch attachment.ContentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return receipt.Recognition{}, fmt.Errorf("receipt model does not support %q; PDF/DOCX conversion is a later module", attachment.ContentType)
	}
	if len(attachment.Data) == 0 {
		return receipt.Recognition{}, fmt.Errorf("receipt image data is empty")
	}
	const prompt = `Extract every visible receipt or invoice field. Return only a JSON object with "outputs" (an object) and "summary" (a string). In outputs include all fields you can identify, using keys title, occasion, country, currency, type, taxrate, tax, total, amountwithouttax, Seller, Sellertaxnumber, Buyer, Buyertaxnumber, Payee, Payer, TypeofBill, DateofInssuance, Remark, Number where applicable; add other useful fields without inventing values. Keep amounts and dates as source text.`
	msg, err := c.api.CreateMessage(ctx, sdk.MessageNewParams{
		Model: sdk.Model(c.model), MaxTokens: 2048,
		Messages: []sdk.MessageParam{sdk.NewUserMessage(
			sdk.NewTextBlock(prompt),
			sdk.NewImageBlockBase64(attachment.ContentType, base64.StdEncoding.EncodeToString(attachment.Data)),
		)},
	})
	if err != nil {
		return receipt.Recognition{}, fmt.Errorf("recognize receipt with model: %w", err)
	}
	var response strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			response.WriteString(block.Text)
		}
	}
	raw := []byte(strings.TrimSpace(response.String()))
	var parsed struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
		Summary string                     `json:"summary"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Outputs == nil {
		return receipt.Recognition{}, fmt.Errorf("receipt model returned no valid JSON outputs")
	}
	return receipt.Recognition{Outputs: parsed.Outputs, Summary: parsed.Summary, Raw: raw}, nil
}
