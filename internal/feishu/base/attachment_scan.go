package base

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ScanAttachmentRecords lists only one configured Bitable table and yields rows
// with non-empty values in the configured attachment field.
func (c *AttachmentClient) ScanAttachmentRecords(ctx context.Context, base, table, fieldID string, yield func(string, map[string]bool) error) error {
	var auth struct {
		Code              int    `json:"code"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := c.request(ctx, http.MethodPost, feishuAPI+"/auth/v3/tenant_access_token/internal", "", map[string]string{"app_id": c.appID, "app_secret": c.appSecret}, &auth); err != nil {
		return err
	}
	if auth.TenantAccessToken == "" {
		return fmt.Errorf("Feishu app authentication failed: code %d", auth.Code)
	}
	root := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s", feishuAPI, url.PathEscape(base), url.PathEscape(table))
	var fields struct {
		Items []struct {
			ID   string `json:"field_id"`
			Name string `json:"field_name"`
			Type int    `json:"type"`
		} `json:"items"`
		HasMore   bool   `json:"has_more"`
		PageToken string `json:"page_token"`
	}
	fieldName, next := "", ""
	for {
		endpoint := root + "/fields?page_size=100"
		if next != "" {
			endpoint += "&page_token=" + url.QueryEscape(next)
		}
		if err := c.request(ctx, http.MethodGet, endpoint, auth.TenantAccessToken, nil, &fields); err != nil {
			return err
		}
		for _, field := range fields.Items {
			if field.ID == fieldID && field.Type == 17 {
				fieldName = field.Name
				break
			}
		}
		if fieldName != "" || !fields.HasMore {
			break
		}
		if fields.PageToken == "" || fields.PageToken == next {
			return fmt.Errorf("Feishu field pagination did not advance")
		}
		next = fields.PageToken
	}
	if fieldName == "" {
		return fmt.Errorf("configured attachment field %s is missing or is not an attachment", fieldID)
	}
	next = ""
	for {
		var page struct {
			Items []struct {
				ID     string                     `json:"record_id"`
				Fields map[string]json.RawMessage `json:"fields"`
			} `json:"items"`
			HasMore   bool   `json:"has_more"`
			PageToken string `json:"page_token"`
		}
		endpoint := root + "/records?page_size=100"
		if next != "" {
			endpoint += "&page_token=" + url.QueryEscape(next)
		}
		if err := c.request(ctx, http.MethodGet, endpoint, auth.TenantAccessToken, nil, &page); err != nil {
			return err
		}
		for _, record := range page.Items {
			var files []struct {
				Token string `json:"file_token"`
			}
			if raw := record.Fields[fieldName]; len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &files); err != nil {
					return fmt.Errorf("decode attachment cell in record %s: %w", record.ID, err)
				}
			}
			tokens := make(map[string]bool, len(files))
			for _, file := range files {
				if file.Token != "" {
					tokens[file.Token] = true
				}
			}
			if len(tokens) > 0 {
				if err := yield(record.ID, tokens); err != nil {
					return err
				}
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.PageToken == "" || page.PageToken == next {
			return fmt.Errorf("Feishu record pagination did not advance")
		}
		next = page.PageToken
	}
}
