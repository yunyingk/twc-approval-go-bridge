// Package anyreceipt adapts the existing Anyreceipt OCR endpoint to receipt.Recognizer.
package anyreceipt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
)

const summaryURL = "https://pi.anyreceipt.cn/api/ocr/summary"

// Client calls the same OCR endpoint as the existing Feishu field shortcut.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

var _ receipt.Recognizer = (*Client)(nil)

func New(apiKey string, httpClient *http.Client) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("Anyreceipt API key is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{apiKey: apiKey, httpClient: httpClient}, nil
}

func (c *Client) Recognize(ctx context.Context, attachment receipt.Attachment) (receipt.Recognition, error) {
	if c == nil {
		return receipt.Recognition{}, fmt.Errorf("Anyreceipt client is not initialized")
	}
	if strings.TrimSpace(attachment.URL) == "" {
		return receipt.Recognition{}, fmt.Errorf("receipt attachment URL is required")
	}

	name := attachment.Name
	if name == "" {
		name = "receipt"
	}
	body := map[string]string{"fileName": name, "url": attachment.URL}
	if attachment.ContentType != "" {
		body["fileType"] = attachment.ContentType
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return receipt.Recognition{}, fmt.Errorf("encode Anyreceipt request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, summaryURL, bytes.NewReader(encoded))
	if err != nil {
		return receipt.Recognition{}, fmt.Errorf("create Anyreceipt request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return receipt.Recognition{}, fmt.Errorf("call Anyreceipt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return receipt.Recognition{}, fmt.Errorf("Anyreceipt returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
		Data    struct {
			Outputs map[string]json.RawMessage `json:"outputs"`
		} `json:"data"`
		ErrorCode    *int   `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return receipt.Recognition{}, fmt.Errorf("decode Anyreceipt response: %w", err)
	}
	if result.ErrorCode != nil || result.ErrorMessage != "" {
		return receipt.Recognition{}, fmt.Errorf("Anyreceipt reported an API error")
	}
	outputs := result.Outputs
	if result.Data.Outputs != nil {
		outputs = result.Data.Outputs
	}
	if outputs == nil {
		return receipt.Recognition{}, fmt.Errorf("Anyreceipt response has no outputs")
	}

	return receipt.Recognition{
		Title:   textValue(outputs["title"]),
		Country: textValue(outputs["country"]),
		Type:    textValue(outputs["type"]),
		Total:   textValue(outputs["total"]),
		Tax:     textValue(outputs["tax"]),
		Date:    textValue(outputs["DateofInssuance"]),
		Number:  textValue(outputs["Number"]),
	}, nil
}

func textValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return ""
	}
	return valueToText(value)
}

func valueToText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			if text := valueToText(item); text != "" {
				items = append(items, text)
			}
		}
		return strings.Join(items, ", ")
	case map[string]any:
		for _, key := range []string{"text", "name", "value", "tmp_url", "url"} {
			if item, ok := v[key]; ok && item != nil {
				return valueToText(item)
			}
		}
		return ""
	default:
		return fmt.Sprint(v)
	}
}
