// Package anyreceipt adapts the existing Anyreceipt OCR endpoint to invoice.Recognizer.
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

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receiptcompat"
)

const summaryURL = "https://pi.anyreceipt.cn/api/ocr/summary"

// Client calls the same OCR endpoint as the existing Feishu field shortcut.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

var _ invoice.Recognizer = (*Client)(nil)

func New(apiKey string, httpClient *http.Client) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("Anyreceipt API key is required")
	}
	if httpClient == nil {
		// Synchronous OCR can exceed 30 seconds; allow room within the flow's
		// two-minute job deadline for download, recognition and ledger delivery.
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	return &Client{apiKey: apiKey, httpClient: httpClient}, nil
}

func (c *Client) Recognize(ctx context.Context, attachment invoice.Attachment) (invoice.Recognition, error) {
	if c == nil {
		return invoice.Recognition{}, fmt.Errorf("Anyreceipt client is not initialized")
	}
	if strings.TrimSpace(attachment.URL) == "" {
		return invoice.Recognition{}, fmt.Errorf("receipt attachment URL is required")
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
		return invoice.Recognition{}, fmt.Errorf("encode Anyreceipt request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, summaryURL, bytes.NewReader(encoded))
	if err != nil {
		return invoice.Recognition{}, fmt.Errorf("create Anyreceipt request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return invoice.Recognition{}, fmt.Errorf("call Anyreceipt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return invoice.Recognition{}, fmt.Errorf("Anyreceipt returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
		Data    struct {
			Outputs map[string]json.RawMessage `json:"outputs"`
		} `json:"data"`
		ErrorCode    *int   `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
		Summary      string `json:"summary"`
		TraceID      string `json:"traceId"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return invoice.Recognition{}, fmt.Errorf("read Anyreceipt response: %w", err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return invoice.Recognition{}, fmt.Errorf("decode Anyreceipt response: %w", err)
	}
	if result.ErrorCode != nil || result.ErrorMessage != "" {
		return invoice.Recognition{}, fmt.Errorf("Anyreceipt reported an API error")
	}
	outputs := result.Outputs
	if result.Data.Outputs != nil {
		outputs = result.Data.Outputs
	}
	if outputs == nil {
		return invoice.Recognition{}, fmt.Errorf("Anyreceipt response has no outputs")
	}

	return receiptcompat.Normalize(invoice.Recognition{
		Origin:  invoice.Origin{Provider: "anyreceipt", TraceID: result.TraceID},
		Title:   textValue(outputs["title"]),
		Country: textValue(outputs["country"]),
		Type:    textValue(outputs["type"]),
		Total:   textValue(outputs["total"]),
		Tax:     textValue(outputs["tax"]),
		Date:    textValue(outputs["DateofInssuance"]),
		Number:  textValue(outputs["Number"]),
		Summary: result.Summary,
		Outputs: outputs,
		Raw:     raw,
	}), nil
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
