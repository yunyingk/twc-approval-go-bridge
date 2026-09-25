// Package anthropic reserves the direct Messages API integration for receipt OCR.
// Mapping attachments and model output into receipt.Recognition is a later step.
package anthropic

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Client exposes the standard Messages API without binding the receipt workflow
// to a model name, prompt, or image transport that has not been chosen yet.
type Client struct {
	sdk sdk.Client
}

func New(apiKey string) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key is required")
	}
	return &Client{sdk: sdk.NewClient(option.WithAPIKey(apiKey))}, nil
}

func (c *Client) CreateMessage(ctx context.Context, params sdk.MessageNewParams) (*sdk.Message, error) {
	if c == nil {
		return nil, fmt.Errorf("Anthropic client is not initialized")
	}
	return c.sdk.Messages.New(ctx, params)
}
