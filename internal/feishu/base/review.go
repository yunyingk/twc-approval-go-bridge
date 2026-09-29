package base

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal/review"
)

// ReviewSource reads only the configured detail and invoice-ledger tables.
// Transaction records are outside this adapter.
type ReviewSource struct {
	client                                                             *LedgerClient
	base, detailTable, attachmentFieldID, detailIDFieldID, ledgerTable string
	ledgerFields                                                       map[string]string
}

func NewReviewSource(appID, appSecret, base, detailTable, attachmentFieldID, detailIDFieldID, ledgerTable string, ledgerFields map[string]string) (*ReviewSource, error) {
	if appID == "" || appSecret == "" || base == "" || detailTable == "" || attachmentFieldID == "" || ledgerTable == "" ||
		ledgerFields["source_key"] == "" || ledgerFields["raw_json"] == "" || ledgerFields["invoice_number"] == "" {
		return nil, fmt.Errorf("Seal review requires Feishu credentials and detail, attachment and ledger field IDs")
	}
	return &ReviewSource{client: NewLedgerClient(appID, appSecret), base: base, detailTable: detailTable,
		attachmentFieldID: attachmentFieldID, detailIDFieldID: detailIDFieldID, ledgerTable: ledgerTable, ledgerFields: ledgerFields}, nil
}

func (s *ReviewSource) ReadDetail(ctx context.Context, recordID string) (review.Detail, error) {
	attachments, tokens, err := s.client.ReadAttachments(ctx, s.base, s.detailTable, recordID, s.attachmentFieldID, nil)
	if err != nil {
		return review.Detail{}, err
	}
	documentSN := recordID
	if s.detailIDFieldID != "" {
		value, err := s.client.ReadTextField(ctx, s.base, s.detailTable, recordID, s.detailIDFieldID)
		if err != nil {
			return review.Detail{}, err
		}
		if strings.TrimSpace(value) != "" {
			documentSN = strings.TrimSpace(value)
		}
	}
	createdAt, err := s.createdAt(ctx, recordID)
	if err != nil {
		return review.Detail{}, err
	}
	files := make([]review.File, 0, len(tokens))
	for index, token := range tokens {
		files = append(files, review.File{Token: token, Attachment: attachments[index]})
	}
	return review.Detail{DocumentID: "feishu:" + s.base + ":" + s.detailTable + ":" + recordID,
		DocumentSN: documentSN, RecordID: recordID, StartTime: createdAt, Files: files}, nil
}

func (s *ReviewSource) createdAt(ctx context.Context, recordID string) (time.Time, error) {
	token, err := s.client.accessToken(ctx)
	if err != nil {
		return time.Time{}, err
	}
	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records/%s", feishuAPI,
		url.PathEscape(s.base), url.PathEscape(s.detailTable), url.PathEscape(recordID))
	var data struct {
		Record struct {
			CreatedTime string `json:"created_time"`
		} `json:"record"`
	}
	if err := s.client.request(ctx, http.MethodGet, endpoint, token, nil, &data); err != nil {
		return time.Time{}, err
	}
	millis, err := strconv.ParseInt(data.Record.CreatedTime, 10, 64)
	if err != nil || millis <= 0 {
		// Some Bitable tenants omit created_time from the record response.
		// startTime is then the audit submission time, not an invoice fact.
		return time.Now(), nil
	}
	return time.UnixMilli(millis), nil
}

func (s *ReviewSource) ReadLedgerEntry(ctx context.Context, sourceKey string) (review.LedgerEntry, error) {
	rows, names, err := s.search(ctx, s.ledgerFields["source_key"], sourceKey, 2)
	if err != nil {
		return review.LedgerEntry{}, err
	}
	if len(rows) != 1 {
		return review.LedgerEntry{}, fmt.Errorf("expected one invoice ledger row for source key, found %d", len(rows))
	}
	row := rows[0]
	rawJSON := fieldText(row.Fields, names[s.ledgerFields["raw_json"]])
	if rawJSON == "" {
		return review.LedgerEntry{}, fmt.Errorf("ledger OCR JSON field is empty in matched record")
	}
	var result struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
		Data    struct {
			Outputs map[string]json.RawMessage `json:"outputs"`
		} `json:"data"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		return review.LedgerEntry{}, fmt.Errorf("decode ledger OCR JSON: %w", err)
	}
	outputs := result.Outputs
	if result.Data.Outputs != nil {
		outputs = result.Data.Outputs
	}
	if outputs == nil {
		return review.LedgerEntry{}, fmt.Errorf("ledger OCR JSON has no outputs")
	}
	return review.LedgerEntry{RecordID: row.ID,
		Recognition: invoice.Recognition{Outputs: outputs, Summary: result.Summary, Raw: json.RawMessage(rawJSON)},
		Facts:       s.invoiceFacts(row, names)}, nil
}

func (s *ReviewSource) FindInvoiceCandidates(ctx context.Context, number string) ([]dupcheck.Invoice, error) {
	rows, names, err := s.search(ctx, s.ledgerFields["invoice_number"], number, 100)
	if err != nil {
		return nil, err
	}
	candidates := make([]dupcheck.Invoice, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, s.invoiceFacts(row, names))
	}
	return candidates, nil
}

type reviewRow struct {
	ID     string                     `json:"record_id"`
	Fields map[string]json.RawMessage `json:"fields"`
}

func (s *ReviewSource) search(ctx context.Context, fieldID, value string, maxRows int) ([]reviewRow, map[string]string, error) {
	token, err := s.client.accessToken(ctx)
	if err != nil {
		return nil, nil, err
	}
	names, err := s.client.fieldNames(ctx, token, s.base, s.ledgerTable)
	if err != nil {
		return nil, nil, err
	}
	fieldName := names[fieldID]
	if fieldName == "" {
		return nil, nil, fmt.Errorf("configured ledger search field is missing")
	}
	root := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records/search?page_size=100", feishuAPI,
		url.PathEscape(s.base), url.PathEscape(s.ledgerTable))
	filter := map[string]any{"filter": map[string]any{"conjunction": "and", "conditions": []map[string]any{{
		"field_name": fieldName, "operator": "is", "value": []string{value},
	}}}}
	var all []reviewRow
	next := ""
	for {
		endpoint := root
		if next != "" {
			endpoint += "&page_token=" + url.QueryEscape(next)
		}
		var page struct {
			Items     []reviewRow `json:"items"`
			HasMore   bool        `json:"has_more"`
			PageToken string      `json:"page_token"`
		}
		if err := s.client.request(ctx, http.MethodPost, endpoint, token, filter, &page); err != nil {
			return nil, nil, err
		}
		all = append(all, page.Items...)
		if len(all) > maxRows {
			return nil, nil, fmt.Errorf("invoice ledger search exceeded %d candidates", maxRows)
		}
		if !page.HasMore {
			return all, names, nil
		}
		if page.PageToken == "" || page.PageToken == next {
			return nil, nil, fmt.Errorf("invoice ledger pagination did not advance")
		}
		next = page.PageToken
	}
}

func (s *ReviewSource) invoiceFacts(row reviewRow, names map[string]string) dupcheck.Invoice {
	get := func(semantic string) string { return fieldText(row.Fields, names[s.ledgerFields[semantic]]) }
	return dupcheck.Invoice{RecordID: row.ID, SourceKey: get("source_key"), Number: get("invoice_number"),
		Seller: get("seller"), Type: get("receipt_type")}
}

func fieldText(fields map[string]json.RawMessage, name string) string {
	if name == "" || len(fields[name]) == 0 {
		return ""
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(fields[name]))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ""
	}
	return strings.TrimSpace(textSegments(value))
}

func textSegments(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case []any:
		var builder strings.Builder
		for _, part := range typed {
			builder.WriteString(textSegments(part))
		}
		return builder.String()
	case map[string]any:
		for _, key := range []string{"text", "value"} {
			if nested, exists := typed[key]; exists {
				return textSegments(nested)
			}
		}
	}
	return ""
}
