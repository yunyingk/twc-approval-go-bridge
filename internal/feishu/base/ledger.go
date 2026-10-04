package base

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// LedgerClient writes only the configured invoice ledger using app identity.
type LedgerClient struct{ *AttachmentClient }

func NewLedgerClient(appID, appSecret string) *LedgerClient {
	return &LedgerClient{AttachmentClient: NewAttachmentClient(appID, appSecret)}
}

func (c *LedgerClient) accessToken(ctx context.Context) (string, error) {
	var auth struct {
		Code              int    `json:"code"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := c.request(ctx, http.MethodPost, feishuAPI+"/auth/v3/tenant_access_token/internal", "", map[string]string{"app_id": c.appID, "app_secret": c.appSecret}, &auth); err != nil {
		return "", err
	}
	if auth.TenantAccessToken == "" {
		return "", fmt.Errorf("Feishu app authentication failed: code %d", auth.Code)
	}
	return auth.TenantAccessToken, nil
}

func (c *LedgerClient) fieldNames(ctx context.Context, token, base, table string) (map[string]string, error) {
	root := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/fields?page_size=100", feishuAPI, url.PathEscape(base), url.PathEscape(table))
	names := make(map[string]string)
	next := ""
	for {
		var page struct {
			Items []struct {
				ID   string `json:"field_id"`
				Name string `json:"field_name"`
			} `json:"items"`
			HasMore   bool   `json:"has_more"`
			PageToken string `json:"page_token"`
		}
		endpoint := root
		if next != "" {
			endpoint += "&page_token=" + url.QueryEscape(next)
		}
		if err := c.request(ctx, http.MethodGet, endpoint, token, nil, &page); err != nil {
			return nil, err
		}
		for _, field := range page.Items {
			names[field.ID] = field.Name
		}
		if !page.HasMore {
			return names, nil
		}
		if page.PageToken == "" || page.PageToken == next {
			return nil, fmt.Errorf("Feishu field pagination did not advance")
		}
		next = page.PageToken
	}
}

// ReadTextField resolves a stable field ID before reading the source record.
func (c *LedgerClient) ReadTextField(ctx context.Context, base, table, recordID, fieldID string) (string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}
	names, err := c.fieldNames(ctx, token, base, table)
	if err != nil {
		return "", err
	}
	name := names[fieldID]
	if name == "" {
		return "", fmt.Errorf("source field %s does not exist", fieldID)
	}
	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records/%s", feishuAPI, url.PathEscape(base), url.PathEscape(table), url.PathEscape(recordID))
	var data struct {
		Record struct {
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"record"`
	}
	if err := c.request(ctx, http.MethodGet, endpoint, token, nil, &data); err != nil {
		return "", err
	}
	var value string
	if raw := data.Record.Fields[name]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("source field %s is not text: %w", fieldID, err)
		}
	}
	return value, nil
}

// UpsertLedgerRecord finds a record by the source-key field and writes only
// supplied field IDs. It never changes records in the source or transaction table.
func (c *LedgerClient) UpsertLedgerRecord(ctx context.Context, base, table, sourceKeyFieldID, sourceKey string, values map[string]any) (string, bool, error) {
	return c.upsertLedgerRecord(ctx, base, table, sourceKeyFieldID, sourceKey, values, false)
}

// UpsertRecognizedInvoice fills newly available columns while preserving all
// existing facts and original evidence, including human-edited empty values.
func (c *LedgerClient) UpsertRecognizedInvoice(ctx context.Context, base, table, sourceKeyFieldID, sourceKey string, values map[string]any) (string, bool, error) {
	return c.upsertLedgerRecord(ctx, base, table, sourceKeyFieldID, sourceKey, values, true)
}

func (c *LedgerClient) upsertLedgerRecord(ctx context.Context, base, table, sourceKeyFieldID, sourceKey string, values map[string]any, preserve bool) (string, bool, error) {
	if base == "" || table == "" || sourceKeyFieldID == "" || sourceKey == "" {
		return "", false, fmt.Errorf("ledger Base, table and source key are required")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", false, err
	}
	names, err := c.fieldNames(ctx, token, base, table)
	if err != nil {
		return "", false, err
	}
	keyName := names[sourceKeyFieldID]
	if keyName == "" {
		return "", false, fmt.Errorf("ledger source-key field %s does not exist", sourceKeyFieldID)
	}
	fields := make(map[string]any, len(values)+1)
	for fieldID, value := range values {
		name := names[fieldID]
		if name == "" {
			return "", false, fmt.Errorf("ledger field %s does not exist", fieldID)
		}
		fields[name] = value
	}
	fields[keyName] = sourceKey
	root := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records", feishuAPI, url.PathEscape(base), url.PathEscape(table))
	filter := map[string]any{"filter": map[string]any{"conjunction": "and", "conditions": []map[string]any{{"field_name": keyName, "operator": "is", "value": []string{sourceKey}}}}}
	var matches struct {
		Items []struct {
			ID     string                     `json:"record_id"`
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	if err := c.request(ctx, http.MethodPost, root+"/search?page_size=2", token, filter, &matches); err != nil {
		return "", false, err
	}
	if len(matches.Items) > 1 || matches.HasMore {
		return "", false, fmt.Errorf("multiple ledger records share the same source key")
	}
	input := map[string]any{"fields": fields}
	if len(matches.Items) == 1 {
		id := matches.Items[0].ID
		if preserve {
			previous := matches.Items[0].Fields
			if previous == nil {
				var data struct {
					Record struct {
						Fields map[string]json.RawMessage `json:"fields"`
					} `json:"record"`
				}
				if err := c.request(ctx, http.MethodGet, root+"/"+url.PathEscape(id), token, nil, &data); err != nil {
					return "", false, err
				}
				previous = data.Record.Fields
			}
			for name := range fields {
				if name != keyName {
					if _, exists := previous[name]; exists {
						delete(fields, name)
					}
				}
			}
		}
		if err := c.request(ctx, http.MethodPut, root+"/"+url.PathEscape(id), token, input, &struct{}{}); err != nil {
			return "", false, err
		}
		return id, false, nil
	}
	// A stable UUIDv4-shaped client token makes a retried create idempotent.
	digest := sha256.Sum256([]byte(base + ":" + table + ":" + sourceKey))
	uuid := digest[:16]
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	clientToken := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
	var created struct {
		Record struct {
			ID string `json:"record_id"`
		} `json:"record"`
	}
	if err := c.request(ctx, http.MethodPost, root+"?client_token="+clientToken, token, input, &created); err != nil {
		return "", false, err
	}
	if created.Record.ID == "" {
		return "", false, fmt.Errorf("Feishu created ledger record without an ID")
	}
	return created.Record.ID, true, nil
}

// ReadMappedFields resolves configured stable IDs, preserving empty values.
func (c *LedgerClient) ReadMappedFields(ctx context.Context, base, table, recordID string, mapping map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(mapping))
	if len(mapping) == 0 {
		return result, nil
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	names, err := c.fieldNames(ctx, token, base, table)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records/%s", feishuAPI, url.PathEscape(base), url.PathEscape(table), url.PathEscape(recordID))
	var data struct {
		Record struct {
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"record"`
	}
	if err := c.request(ctx, http.MethodGet, endpoint, token, nil, &data); err != nil {
		return nil, err
	}
	for semantic, id := range mapping {
		name := names[id]
		if semantic == "" || name == "" {
			return nil, fmt.Errorf("configured context field %s is missing", semantic)
		}
		result[semantic] = fieldText(data.Record.Fields, name)
	}
	return result, nil
}
