package card

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

// NotifyResult contains metadata about a successfully sent transaction reminder card.
type NotifyResult struct {
	Status        string `json:"status"`
	MessageID     string `json:"message_id,omitempty"`
	Recipient     string `json:"recipient,omitempty"`
	OpenID        string `json:"open_id,omitempty"`
	TransactionID string `json:"transaction_id,omitempty"`
	Merchant      string `json:"merchant,omitempty"`
	BookedAmount  string `json:"booked_amount,omitempty"`
	FormURL       string `json:"form_url,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// NotifyOptions customizes notification behavior.
type NotifyOptions struct {
	Force bool // If true, ignores existing reimbursement detail linkage and sends anyway.
}

// Service orchestrates reading transactions and sending interactive message cards.
type Service struct {
	cfg    config.Config
	client *Client
}

// NewService instantiates a card service using validated runtime and business configuration.
func NewService(cfg config.Config) (*Service, error) {
	if !cfg.FeishuEnabled() {
		return nil, fmt.Errorf("feishu credentials (app_id, app_secret) are required")
	}
	if cfg.Business == nil {
		return nil, fmt.Errorf("business table profile is required")
	}
	return &Service{
		cfg:    cfg,
		client: NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret),
	}, nil
}

// NotifyTransaction reads a specific transaction record from Bitable, formats the notice,
// and sends an interactive card to the cardholder with a prefilled reimbursement form link.
// If the transaction is already linked to a reimbursement detail, it skips sending unless opts.Force is true.
func (s *Service) NotifyTransaction(ctx context.Context, transactionRecordID string, opts ...NotifyOptions) (*NotifyResult, error) {
	if strings.TrimSpace(transactionRecordID) == "" {
		return nil, fmt.Errorf("transactionRecordID is required")
	}

	transBinding := s.cfg.Business.Tables.Transactions
	detailsBinding := s.cfg.Business.Tables.ReimbursementDetails

	if transBinding.BaseToken == "" || transBinding.TableID == "" {
		return nil, fmt.Errorf("transactions table binding is missing base_token or table_id")
	}

	token, err := s.client.accessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch access token: %w", err)
	}

	fieldMap, err := s.fetchFieldMap(ctx, token, transBinding.BaseToken, transBinding.TableID)
	if err != nil {
		return nil, fmt.Errorf("fetch field schema: %w", err)
	}

	record, err := s.fetchRecord(ctx, token, transBinding.BaseToken, transBinding.TableID, transactionRecordID)
	if err != nil {
		return nil, fmt.Errorf("fetch record %s: %w", transactionRecordID, err)
	}

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

	cardholderRaw := getFieldValue("cardholder")
	if cardholderRaw == nil {
		cardholderRaw = record["持卡人"]
	}
	openID, recipientName := extractCardholderOpenID(cardholderRaw)
	if openID == "" {
		return nil, fmt.Errorf("no cardholder open_id found in transaction record %s", transactionRecordID)
	}

	txID := formatString(getFieldValue("transaction_id"), record["交易流水号"])
	merchant := formatString(getFieldValue("merchant"), record["商户名称"])
	txTime := formatTime(getFieldValue("transaction_time"), record["交易时间"])

	// Deduplication: check if already linked to reimbursement details
	detailRelRaw := getFieldValue("detail_relation")
	if detailRelRaw == nil {
		detailRelRaw = record["个人报销单号"]
	}
	force := len(opts) > 0 && opts[0].Force
	if !force && isAlreadyLinked(detailRelRaw) {
		return &NotifyResult{
			Status:        "skipped_already_linked",
			Recipient:     recipientName,
			OpenID:        openID,
			TransactionID: txID,
			Merchant:      merchant,
			Reason:        "该交易流水已关联个人报销明细，无需重复催报",
		}, nil
	}

	bookedAmt := formatString(getFieldValue("booked_amount_cny"), record["结算金额"])
	bookedCur := formatString(record["结算金额币种"], "CNY")
	bookedAmountStr := strings.TrimSpace(fmt.Sprintf("%s %s", bookedCur, bookedAmt))

	origAmt := formatString(getFieldValue("original_amount"), record["交易金额"])
	origCur := formatString(getFieldValue("original_currency"), record["交易金额币种"])
	origAmountStr := strings.TrimSpace(fmt.Sprintf("%s %s", origCur, origAmt))

	formURL := detailsBinding.FormPrefillURL(s.cfg.Feishu.Host, "关联交易流水号", transactionRecordID)

	notice := TransactionNotice{
		TransactionID:   txID,
		TransactionTime: txTime,
		Merchant:        merchant,
		BookedAmount:    bookedAmountStr,
		OriginalAmount:  origAmountStr,
		FormURL:         formURL,
	}

	msgID, err := s.client.SendCardToUser(ctx, openID, notice)
	if err != nil {
		return nil, fmt.Errorf("send card to %s (%s): %w", recipientName, openID, err)
	}

	return &NotifyResult{
		Status:        "success",
		MessageID:     msgID,
		Recipient:     recipientName,
		OpenID:        openID,
		TransactionID: txID,
		Merchant:      merchant,
		BookedAmount:  bookedAmountStr,
		FormURL:       formURL,
	}, nil
}

func (s *Service) fetchFieldMap(ctx context.Context, token, baseToken, tableID string) (map[string]string, error) {
	url := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/fields?page_size=100", s.client.baseURL, baseToken, tableID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.httpClient.Do(req)
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

func (s *Service) fetchRecord(ctx context.Context, token, baseToken, tableID, recordID string) (map[string]any, error) {
	apiURL := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records/%s", s.client.baseURL, url.PathEscape(baseToken), url.PathEscape(tableID), url.PathEscape(recordID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.httpClient.Do(req)
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

func isAlreadyLinked(raw any) bool {
	if raw == nil {
		return false
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return false
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if rids, ok := m["record_ids"].([]any); ok && len(rids) > 0 {
			return true
		}
		if text, ok := m["text"].(string); ok && strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}
