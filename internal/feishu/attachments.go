package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

const feishuAPI = "https://open.feishu.cn/open-apis"
const maxAttachmentSize = 20 << 20

// AttachmentClient reads only the configured Bitable attachment field using app identity.
type AttachmentClient struct {
	appID, appSecret string
	httpClient       *http.Client
}

func NewAttachmentClient(appID, appSecret string) *AttachmentClient {
	return &AttachmentClient{appID: appID, appSecret: appSecret, httpClient: &http.Client{Timeout: 45 * time.Second}}
}

func (c *AttachmentClient) ReadAttachments(ctx context.Context, base, table, record, fieldID string, selected map[string]bool) ([]invoice.Attachment, []string, error) {
	var auth struct {
		Code              int    `json:"code"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := c.request(ctx, http.MethodPost, feishuAPI+"/auth/v3/tenant_access_token/internal", "", map[string]string{"app_id": c.appID, "app_secret": c.appSecret}, &auth); err != nil {
		return nil, nil, err
	}
	if auth.Code != 0 || auth.TenantAccessToken == "" {
		return nil, nil, fmt.Errorf("Feishu app authentication failed: code %d", auth.Code)
	}
	token := auth.TenantAccessToken
	root := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s", feishuAPI, url.PathEscape(base), url.PathEscape(table))
	var fields struct {
		Items []struct {
			ID   string `json:"field_id"`
			Name string `json:"field_name"`
			Type int    `json:"type"`
		} `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, root+"/fields?page_size=100", token, nil, &fields); err != nil {
		return nil, nil, err
	}
	fieldName := ""
	for _, field := range fields.Items {
		if field.ID == fieldID && field.Type == 17 {
			fieldName = field.Name
			break
		}
	}
	if fieldName == "" {
		return nil, nil, fmt.Errorf("configured attachment field %s is missing or is not an attachment", fieldID)
	}
	var recordData struct {
		Record struct {
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"record"`
	}
	if err := c.request(ctx, http.MethodGet, root+"/records/"+url.PathEscape(record), token, nil, &recordData); err != nil {
		return nil, nil, err
	}
	var files []struct {
		Token string `json:"file_token"`
		Name  string `json:"name"`
		URL   string `json:"tmp_url"`
		Size  int64  `json:"size"`
	}
	if raw := recordData.Record.Fields[fieldName]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &files); err != nil {
			return nil, nil, fmt.Errorf("decode attachment cell: %w", err)
		}
	}
	attachments, tokens := make([]invoice.Attachment, 0, len(files)), make([]string, 0, len(files))
	for _, file := range files {
		if selected != nil && !selected[file.Token] {
			continue
		}
		if file.Token == "" || file.URL == "" {
			return nil, nil, fmt.Errorf("attachment has no file token or temporary URL")
		}
		if file.Size > maxAttachmentSize {
			return nil, nil, fmt.Errorf("attachment exceeds 20 MiB limit")
		}
		parsed, err := url.Parse(file.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "open.feishu.cn" {
			return nil, nil, fmt.Errorf("unexpected Feishu temporary URL")
		}
		var linkData struct {
			TmpDownloadURLs []struct {
				FileToken string `json:"file_token"`
				URL       string `json:"tmp_download_url"`
			} `json:"tmp_download_urls"`
		}
		if err := c.request(ctx, http.MethodGet, file.URL, token, nil, &linkData); err != nil {
			return nil, nil, err
		}
		link := ""
		for _, item := range linkData.TmpDownloadURLs {
			if item.FileToken == file.Token {
				link = item.URL
				break
			}
		}
		if link == "" {
			return nil, nil, fmt.Errorf("Feishu did not provide a download URL for attachment")
		}
		data, mediaType, err := c.downloadAndCheck(ctx, link)
		if err != nil {
			return nil, nil, err
		}
		attachments = append(attachments, invoice.Attachment{Name: file.Name, ContentType: mediaType, URL: link, Data: data})
		tokens = append(tokens, file.Token)
	}
	return attachments, tokens, nil
}

func (c *AttachmentClient) request(ctx context.Context, method, endpoint, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call Feishu: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Feishu HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Code              int             `json:"code"`
		Msg               string          `json:"msg"`
		Data              json.RawMessage `json:"data"`
		TenantAccessToken string          `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return fmt.Errorf("Feishu API code %d: %s", envelope.Code, envelope.Msg)
	}
	if len(envelope.Data) > 0 {
		return json.Unmarshal(envelope.Data, output)
	}
	if envelope.TenantAccessToken != "" {
		return json.Unmarshal([]byte(fmt.Sprintf(`{"code":0,"tenant_access_token":%q}`, envelope.TenantAccessToken)), output)
	}
	return nil
}

func (c *AttachmentClient) downloadAndCheck(ctx context.Context, link string) ([]byte, string, error) {
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, "", fmt.Errorf("invalid attachment download URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download attachment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("attachment download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxAttachmentSize {
		return nil, "", fmt.Errorf("attachment exceeds 20 MiB limit")
	}
	mediaType := strings.Split(http.DetectContentType(data), ";")[0]
	switch mediaType {
	case "application/pdf", "image/jpeg", "image/png", "image/gif", "image/webp":
		return data, mediaType, nil
	default:
		return nil, "", fmt.Errorf("attachment content type %q is not allowlisted", mediaType)
	}
}
