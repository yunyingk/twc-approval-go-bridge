package card

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/task"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

// NotifyResult contains metadata about a successfully sent transaction reminder card.
type NotifyResult struct {
	Status         string `json:"status"`
	MessageID      string `json:"message_id,omitempty"`
	Recipient      string `json:"recipient,omitempty"`
	OpenID         string `json:"open_id,omitempty"`
	TransactionID  string `json:"transaction_id,omitempty"`
	Merchant       string `json:"merchant,omitempty"`
	BookedAmount   string `json:"booked_amount,omitempty"`
	FormURL        string `json:"form_url,omitempty"`
	DetailRecordID string `json:"detail_record_id,omitempty"`
	TaskGUID       string `json:"task_guid,omitempty"`
	RecordURL      string `json:"record_url,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// NotifyOptions customizes notification behavior.
type NotifyOptions struct {
	Force bool // If true, ignores existing reimbursement detail linkage and sends anyway.
}

// Service orchestrates reading transactions and sending interactive message cards.
type Service struct {
	cfg        config.Config
	client     *Client
	taskClient *task.Client
	taskSvc    *task.Service
	mu         sync.Mutex
	botName    string
}

// NewService instantiates a card service using validated runtime and business configuration.
func NewService(cfg config.Config, taskSvc ...*task.Service) (*Service, error) {
	if !cfg.FeishuEnabled() {
		return nil, fmt.Errorf("feishu credentials (app_id, app_secret) are required")
	}
	if cfg.Business == nil {
		return nil, fmt.Errorf("business table profile is required")
	}

	svc := &Service{
		cfg:    cfg,
		client: NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret),
	}

	if len(taskSvc) > 0 && taskSvc[0] != nil {
		svc.taskSvc = taskSvc[0]
		svc.taskClient = task.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	} else if cfg.FeishuEnabled() {
		var store task.Store
		if cfg.Runtime.StateDir != "" {
			if st, err := state.NewFiles(cfg.Runtime.StateDir); err == nil {
				store = st
			}
		}
		svc.taskClient = task.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
		svc.taskSvc = task.NewService(svc.taskClient, store)
	}

	return svc, nil
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

	targetURL := detailsBinding.FormPrefillURL(s.cfg.Feishu.Host, "关联交易流水号", transactionRecordID)
	botName := s.getBotName(ctx)
	noteText := "来自 海外易商卡报销助手"
	if botName != "" {
		noteText = fmt.Sprintf("来自 海外易商卡 × %s", botName)
	}

	var detailRecordID string
	var taskGUID string

	// Option 1: prefill detail record and task when reimbursement details table is configured
	if detailsBinding.BaseToken != "" && detailsBinding.TableID != "" {
		detailRecordID, _ = s.createPrefillDetail(ctx, token, detailsBinding, transactionRecordID, openID, merchant)
		if detailRecordID != "" {
			host := strings.TrimRight(s.cfg.Feishu.Host, "/")
			if host == "" {
				host = "https://open.feishu.cn"
			}
			targetURL = fmt.Sprintf("%s/base/%s?table=%s&record=%s", host, detailsBinding.BaseToken, detailsBinding.TableID, detailRecordID)

			if s.taskClient != nil {
				taskGUID, _ = s.createNoticeTask(ctx, targetURL, openID, txID, merchant, bookedAmountStr, origAmountStr, txTime)
				if taskGUID != "" && s.taskSvc != nil {
					_ = s.taskSvc.LinkRecordTask(ctx, detailRecordID, taskGUID)
					if botName != "" {
						noteText = fmt.Sprintf("来自 海外易商卡 × %s · 已同步生成飞书待办", botName)
					} else {
						noteText = "来自 海外易商卡报销助手 · 已同步生成飞书待办"
					}
				}
			}
		}
	}

	notice := TransactionNotice{
		TransactionID:   txID,
		TransactionTime: txTime,
		Merchant:        merchant,
		BookedAmount:    bookedAmountStr,
		OriginalAmount:  origAmountStr,
		FormURL:         targetURL,
		NoteText:        noteText,
	}

	msgID, err := s.client.SendCardToUser(ctx, openID, notice)
	if err != nil {
		return nil, fmt.Errorf("send card to %s (%s): %w", recipientName, openID, err)
	}

	return &NotifyResult{
		Status:         "success",
		MessageID:      msgID,
		Recipient:      recipientName,
		OpenID:         openID,
		TransactionID:  txID,
		Merchant:       merchant,
		BookedAmount:   bookedAmountStr,
		FormURL:        targetURL,
		DetailRecordID: detailRecordID,
		TaskGUID:       taskGUID,
		RecordURL:      targetURL,
	}, nil
}

func (s *Service) createPrefillDetail(ctx context.Context, token string, detailsBinding config.TableBinding, txRecordID, openID, merchant string) (string, error) {
	relField := detailsBinding.Fields["transaction_relation"]
	if relField == "" {
		relField = "关联流水号"
	}
	empField := detailsBinding.Fields["employee"]
	if empField == "" {
		empField = "报销人"
	}
	reasonField := detailsBinding.Fields["expense_reason"]
	if reasonField == "" {
		reasonField = "消费事由"
	}

	fieldMap, err := s.fetchFieldMap(ctx, token, detailsBinding.BaseToken, detailsBinding.TableID)
	if err == nil {
		if name, ok := fieldMap[relField]; ok && name != "" {
			relField = name
		}
		if name, ok := fieldMap[empField]; ok && name != "" {
			empField = name
		}
		if name, ok := fieldMap[reasonField]; ok && name != "" {
			reasonField = name
		}
	}

	payload := map[string]any{
		"fields": map[string]any{
			relField: []string{txRecordID},
			empField: []map[string]string{{"id": openID}},
			reasonField: fmt.Sprintf("%s 消费（待补发票）", merchant),
		},
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	apiURL := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records", s.client.baseURL, url.PathEscape(detailsBinding.BaseToken), url.PathEscape(detailsBinding.TableID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Record struct {
				RecordID string `json:"record_id"`
			} `json:"record"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("create detail record failed (code %d): %s", result.Code, result.Msg)
	}
	return result.Data.Record.RecordID, nil
}

func (s *Service) createNoticeTask(ctx context.Context, recordURL, openID, txID, merchant, bookedAmt, origAmt, txTime string) (string, error) {
	if s.taskClient == nil {
		return "", nil
	}
	now := time.Now()
	due := now.AddDate(0, 0, 7)

	description := fmt.Sprintf(`📌 消费详情核对：
• 交易流水号：%s
• 消费商户：%s
• 交易日期：%s
• 原币扣款：%s
• 结算折算：%s

📑 补票操作指南：
1. 请点击任务上方的【来源链接】或直接点击报销单据链接：
   %s
2. 进入多维表格后，请在「上传发票」一栏上传该笔消费的原始凭证（支持发票、消费水单、Receipt PDF 或清晰截图）。
3. 请在「消费事由」简要备注具体使用场景。

⚠️ 合规与自动完成说明：
• 根据企业财务规范，请在消费发生后 7 天内完成凭证补充。
• 凭证上传完成后，系统将自动核销并结束本待办任务。`,
		txID, merchant, txTime, origAmt, bookedAmt, recordURL)

	summary := fmt.Sprintf("[海外易商卡补票] 请及时补充发票 · %s (%s)", merchant, origAmt)

	return s.taskClient.CreateTask(ctx, task.CreateTaskParam{
		Summary:        summary,
		Description:    description,
		AssigneeOpenID: openID,
		StartTime:      &now,
		DueTime:        &due,
		OriginTitle:    "点击直达报销单据",
		OriginURL:      recordURL,
		PlatformName:   "海外易商卡报销助手",
	})
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

func (s *Service) getBotName(ctx context.Context) string {
	s.mu.Lock()
	if s.botName != "" {
		name := s.botName
		s.mu.Unlock()
		return name
	}
	s.mu.Unlock()

	name, err := s.client.FetchBotName(ctx)
	if err == nil && strings.TrimSpace(name) != "" {
		s.mu.Lock()
		s.botName = strings.TrimSpace(name)
		s.mu.Unlock()
		return strings.TrimSpace(name)
	}
	return ""
}
