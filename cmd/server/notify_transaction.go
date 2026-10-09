package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/card"
)

type notifyResult struct {
	Status        string `json:"status"`
	MessageID     string `json:"message_id"`
	Recipient     string `json:"recipient"`
	OpenID        string `json:"open_id"`
	TransactionID string `json:"transaction_id"`
	Merchant      string `json:"merchant"`
	BookedAmount  string `json:"booked_amount"`
	FormURL       string `json:"form_url"`
}

func runNotifyTransaction(ctx context.Context, cfg config.Config, transactionRecordID string, output io.Writer) error {
	if cfg.Business == nil || !cfg.FeishuEnabled() {
		return fmt.Errorf("notify-transaction requires complete configuration and Feishu app credentials")
	}

	appID := cfg.Feishu.AppID
	appSecret := cfg.Feishu.AppSecret
	transBinding := cfg.Business.Tables.Transactions
	detailsBinding := cfg.Business.Tables.ReimbursementDetails

	if transBinding.BaseToken == "" || transBinding.TableID == "" {
		return fmt.Errorf("transactions table binding is missing base_token or table_id")
	}

	// 1. Fetch access token
	token, err := fetchTenantToken(ctx, appID, appSecret)
	if err != nil {
		return fmt.Errorf("fetch tenant token: %w", err)
	}

	// 2. Fetch table fields schema to map field IDs to field names
	fieldMap, err := fetchTableFieldNames(ctx, token, transBinding.BaseToken, transBinding.TableID)
	if err != nil {
		return fmt.Errorf("fetch transactions field schema: %w", err)
	}

	// 3. Fetch transaction record
	record, err := fetchBitableRecord(ctx, token, transBinding.BaseToken, transBinding.TableID, transactionRecordID)
	if err != nil {
		return fmt.Errorf("fetch transaction record %s: %w", transactionRecordID, err)
	}

	// Helper to extract value using field semantic key
	getFieldValue := func(semantic string) any {
		fieldID := transBinding.Fields[semantic]
		if fieldID == "" {
			return nil
		}
		fieldName := fieldMap[fieldID]
		if fieldName == "" {
			return nil
		}
		return record[fieldName]
	}

	// 4. Extract cardholder
	cardholderRaw := getFieldValue("cardholder")
	if cardholderRaw == nil {
		// Fallback to literal name if not mapped
		cardholderRaw = record["持卡人"]
	}
	openID, recipientName := extractCardholderOpenID(cardholderRaw)
	if openID == "" {
		return fmt.Errorf("no cardholder open_id found in transaction record %s", transactionRecordID)
	}

	// 5. Extract transaction facts
	txID := formatString(getFieldValue("transaction_id"), record["交易流水号"])
	merchant := formatString(getFieldValue("merchant"), record["商户名称"])
	txTime := formatTime(getFieldValue("transaction_time"), record["交易时间"])

	bookedAmt := formatString(getFieldValue("booked_amount_cny"), record["结算金额"])
	bookedCur := formatString(record["结算金额币种"], "CNY")
	bookedAmountStr := strings.TrimSpace(fmt.Sprintf("%s %s", bookedCur, bookedAmt))

	origAmt := formatString(getFieldValue("original_amount"), record["交易金额"])
	origCur := formatString(getFieldValue("original_currency"), record["交易金额币种"])
	origAmountStr := strings.TrimSpace(fmt.Sprintf("%s %s", origCur, origAmt))

	// 6. Build Form URL with prefill
	formURL := detailsBinding.FormPrefillURL(cfg.Feishu.Host, "关联交易流水号", transactionRecordID)

	notice := card.TransactionNotice{
		TransactionID:   txID,
		TransactionTime: txTime,
		Merchant:        merchant,
		BookedAmount:    bookedAmountStr,
		OriginalAmount:  origAmountStr,
		FormURL:         formURL,
	}

	// 7. Dispatch card
	cardClient := card.NewClient(appID, appSecret)
	msgID, err := cardClient.SendCardToUser(ctx, openID, notice)
	if err != nil {
		return fmt.Errorf("send card to %s (%s): %w", recipientName, openID, err)
	}

	res := notifyResult{
		Status:        "success",
		MessageID:     msgID,
		Recipient:     recipientName,
		OpenID:        openID,
		TransactionID: txID,
		Merchant:      merchant,
		BookedAmount:  bookedAmountStr,
		FormURL:       formURL,
	}

	return json.NewEncoder(output).Encode(res)
}

func fetchTenantToken(ctx context.Context, appID, appSecret string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", strings.NewReader(string(reqBody)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 || result.TenantAccessToken == "" {
		return "", fmt.Errorf("auth error (%d): %s", result.Code, result.Msg)
	}
	return result.TenantAccessToken, nil
}

func fetchTableFieldNames(ctx context.Context, token, baseToken, tableID string) (map[string]string, error) {
	url := fmt.Sprintf("https://open.feishu.cn/open-apis/bitable/v1/apps/%s/tables/%s/fields?page_size=100", baseToken, tableID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				FieldID   string `json:"field_id"`
				FieldName string `json:"field_name"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("list fields failed (code %d)", result.Code)
	}

	mapping := make(map[string]string, len(result.Data.Items))
	for _, item := range result.Data.Items {
		mapping[item.FieldID] = item.FieldName
	}
	return mapping, nil
}

func fetchBitableRecord(ctx context.Context, token, baseToken, tableID, recordID string) (map[string]any, error) {
	apiURL := fmt.Sprintf("https://open.feishu.cn/open-apis/bitable/v1/apps/%s/tables/%s/records/%s", url.PathEscape(baseToken), url.PathEscape(tableID), url.PathEscape(recordID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Record struct {
				Fields map[string]any `json:"fields"`
			} `json:"record"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("get record failed (code %d): %s", result.Code, result.Msg)
	}
	return result.Data.Record.Fields, nil
}

func extractCardholderOpenID(raw any) (string, string) {
	if raw == nil {
		return "", ""
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return "", ""
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		return "", ""
	}
	id, _ := first["id"].(string)
	name, _ := first["name"].(string)
	return id, name
}

func formatString(vals ...any) string {
	for _, v := range vals {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if strings.TrimSpace(val) != "" {
				return strings.TrimSpace(val)
			}
		case []any:
			if len(val) > 0 {
				if s, ok := val[0].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
				if m, ok := val[0].(map[string]any); ok {
					if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
						return strings.TrimSpace(t)
					}
				}
			}
		}
	}
	return ""
}

func formatTime(vals ...any) string {
	for _, v := range vals {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case float64:
			if val > 0 {
				t := time.UnixMilli(int64(val))
				return t.Format("2006/01/02")
			}
		case int64:
			if val > 0 {
				t := time.UnixMilli(val)
				return t.Format("2006/01/02")
			}
		case string:
			if strings.TrimSpace(val) != "" {
				return strings.TrimSpace(val)
			}
		}
	}
	return ""
}
