// Package anthropic exposes the standard Messages API as an independent AI boundary.
// Receipt recognition may use it later, without defining its prompt or output here.
package anthropic

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Client exposes the standard Messages API without binding callers to a model
// name, prompt, or image transport that has not been chosen yet.
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
