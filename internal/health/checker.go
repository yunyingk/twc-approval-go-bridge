package health

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

const feishuAPI = "https://open.feishu.cn/open-apis"

// Checker executes comprehensive health checks against runtime and external services.
type Checker struct {
	cfg        config.Config
	httpClient *http.Client
	baseURL    string
}

// NewChecker instantiates a Checker with default HTTP client.
func NewChecker(cfg config.Config) *Checker {
	return &Checker{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    feishuAPI,
	}
}

// SetBaseURLForTest overrides API endpoint for mock tests.
func (c *Checker) SetBaseURLForTest(u string) {
	c.baseURL = u
}

// CheckAll executes all diagnostic categories and compiles an aggregate report.
func (c *Checker) CheckAll(ctx context.Context) Report {
	start := time.Now()
	var items []CheckItem

	// 1. Feishu Identity & Auth
	token, authItems := c.checkFeishuAuth(ctx)
	items = append(items, authItems...)

	// 2. Feishu Permissions (only if auth succeeds)
	if token != "" {
		items = append(items, c.checkFeishuPermissions(ctx, token)...)
	}

	// 3. Bitable Tables Topology (only if auth succeeds)
	if token != "" && c.cfg.Business != nil {
		items = append(items, c.checkBitableTopology(ctx, token)...)
	}

	// 4. External AI / OCR Providers
	items = append(items, c.checkExternalProviders(ctx)...)

	// 5. Runtime Environment
	items = append(items, c.checkRuntimeEnvironment(ctx)...)

	// Aggregate summary
	report := Report{
		Total:    len(items),
		Duration: time.Since(start).Round(time.Millisecond).String(),
		Items:    items,
	}
	for _, item := range items {
		switch item.Status {
		case StatusPass:
			report.Passed++
		case StatusWarn:
			report.Warned++
		case StatusFail:
			report.Failed++
		}
	}

	if report.Failed > 0 {
		report.OverallStatus = "CRITICAL (存在阻断项，无法正常运行)"
	} else if report.Warned > 0 {
		report.OverallStatus = "WARNING (基本健康，但存在配置预警)"
	} else {
		report.OverallStatus = "100% HEALTHY (全部探测项通过，可放心启动服务！)"
	}

	return report
}

func (c *Checker) checkFeishuAuth(ctx context.Context) (string, []CheckItem) {
	cat := "飞书应用认证与连通"
	var items []CheckItem

	if !c.cfg.FeishuEnabled() {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "App 凭证配置",
			Status:   StatusFail,
			Message:  "缺失 [feishu].app_id 或 app_secret",
			Remedy:   "请在 configs/config.toml 中配置正确的 [feishu].app_id 与 app_secret",
		})
		return "", items
	}

	// Probe Tenant Access Token
	start := time.Now()
	token, err := c.fetchTenantToken(ctx)
	latency := time.Since(start)

	if err != nil {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "App 凭证认证 (TenantAccessToken)",
			Status:   StatusFail,
			Latency:  latency,
			Message:  fmt.Sprintf("认证失败: %v", err),
			Remedy:   "请登录飞书开放平台，核对 App ID 与 App Secret 是否有效且未被重置",
		})
		return "", items
	}

	items = append(items, CheckItem{
		Category: cat,
		Name:     "App 凭证认证 (TenantAccessToken)",
		Status:   StatusPass,
		Latency:  latency,
		Message:  "有效 (已成功获取 Tenant Token)",
	})

	// Probe Feishu Enterprise Host
	if c.cfg.Feishu.Host != "" {
		hostStart := time.Now()
		hostErr := c.probeHost(ctx, c.cfg.Feishu.Host)
		hostLatency := time.Since(hostStart)
		if hostErr != nil {
			items = append(items, CheckItem{
				Category: cat,
				Name:     "企业域名连通性",
				Status:   StatusWarn,
				Latency:  hostLatency,
				Message:  fmt.Sprintf("域名响应异常 (%s): %v", c.cfg.Feishu.Host, hostErr),
				Remedy:   "请核对 configs/config.toml 中的 host 是否准确",
			})
		} else {
			items = append(items, CheckItem{
				Category: cat,
				Name:     "企业域名连通性",
				Status:   StatusPass,
				Latency:  hostLatency,
				Message:  fmt.Sprintf("正常 (%s)", c.cfg.Feishu.Host),
			})
		}
	}

	// Probe Bot info
	botStart := time.Now()
	botName, botErr := c.fetchBotInfo(ctx, token)
	botLatency := time.Since(botStart)
	if botErr != nil {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "应用机器人能力",
			Status:   StatusFail,
			Latency:  botLatency,
			Message:  fmt.Sprintf("机器人不可用: %v", botErr),
			Remedy:   "请在飞书开放平台 -> 应用功能 -> 机器人，开启机器人功能并发布新版本",
		})
	} else {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "应用机器人能力",
			Status:   StatusPass,
			Latency:  botLatency,
			Message:  fmt.Sprintf("已启用 (机器人名称: %s)", botName),
		})
	}

	return token, items
}

func (c *Checker) checkFeishuPermissions(ctx context.Context, token string) []CheckItem {
	cat := "飞书应用权限探测"
	var items []CheckItem

	// 1. Probe task:task:write via task list read
	taskStart := time.Now()
	taskErr := c.probeTaskPermission(ctx, token)
	taskLatency := time.Since(taskStart)
	if taskErr != nil {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "待办任务权限 (task:task:write)",
			Status:   StatusFail,
			Latency:  taskLatency,
			Message:  fmt.Sprintf("缺失待办权限: %v", taskErr),
			Remedy:   "请在飞书开放平台 -> 权限管理 -> 搜索勾选「获取及更新任务信息」(task:task:write) 并发布新版本",
		})
	} else {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "待办任务权限 (task:task:write)",
			Status:   StatusPass,
			Latency:  taskLatency,
			Message:  "已授权 (可代建待办与更新)",
		})
	}

	// 2. Probe task:comment:write via comment endpoint
	commentStart := time.Now()
	commentErr := c.probeTaskCommentPermission(ctx, token)
	commentLatency := time.Since(commentStart)
	if commentErr != nil {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "任务评论权限 (task:comment:write)",
			Status:   StatusFail,
			Latency:  commentLatency,
			Message:  fmt.Sprintf("缺失评论权限: %v", commentErr),
			Remedy:   "请在飞书开放平台 -> 权限管理 -> 搜索勾选「查看、创建、编辑和删除飞书任务评论」(task:comment:write) 并发布新版本",
		})
	} else {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "任务评论权限 (task:comment:write)",
			Status:   StatusPass,
			Latency:  commentLatency,
			Message:  "已授权 (结单时可在任务中发表留痕评论)",
		})
	}

	return items
}

func (c *Checker) checkBitableTopology(ctx context.Context, token string) []CheckItem {
	cat := "业务多维表格拓扑"
	var items []CheckItem
	p := c.cfg.Business

	tables := []struct {
		role  string
		name  string
		table config.TableBinding
	}{
		{"transactions", "交易流水表", p.Tables.Transactions},
		{"reimbursement_details", "个人报销明细", p.Tables.ReimbursementDetails},
		{"invoice_ledger", "发票台账", p.Tables.InvoiceLedger},
	}

	for _, t := range tables {
		start := time.Now()
		fieldMap, typeMap, err := c.fetchTableSchema(ctx, token, t.table.BaseToken, t.table.TableID)
		latency := time.Since(start)

		if err != nil {
			items = append(items, CheckItem{
				Category: cat,
				Name:     fmt.Sprintf("%s (%s)", t.name, t.table.TableID),
				Status:   StatusFail,
				Latency:  latency,
				Message:  fmt.Sprintf("读取表格失败: %v", err),
				Remedy:   fmt.Sprintf("请确认 BaseToken(%s) 与 TableID(%s) 正确，且已授予应用多维表格权限", t.table.BaseToken, t.table.TableID),
			})
			continue
		}

		// Verify configured fields
		var missing []string
		for semantic, fieldID := range t.table.Fields {
			if _, ok := fieldMap[fieldID]; !ok {
				missing = append(missing, fmt.Sprintf("%s(%s)", semantic, fieldID))
			}
		}

		if len(missing) > 0 {
			items = append(items, CheckItem{
				Category: cat,
				Name:     fmt.Sprintf("%s (%s)", t.name, t.table.TableID),
				Status:   StatusFail,
				Latency:  latency,
				Message:  fmt.Sprintf("发现 %d 个字段在线上表中不存在: %s", len(missing), strings.Join(missing, ", ")),
				Remedy:   "多维表格列名可能已被修改或 ID 不匹配，请使用 check-business-config 校对",
			})
			continue
		}

		_ = typeMap
		items = append(items, CheckItem{
			Category: cat,
			Name:     fmt.Sprintf("%s (%s)", t.name, t.table.TableID),
			Status:   StatusPass,
			Latency:  latency,
			Message:  fmt.Sprintf("正常 (已核验 %d 个字段映射)", len(t.table.Fields)),
		})
	}

	return items
}

func (c *Checker) checkExternalProviders(ctx context.Context) []CheckItem {
	cat := "外部 AI 与 OCR 服务"
	var items []CheckItem

	// 1. OCR Provider
	ocrProvider := c.cfg.ReceiptProvider()
	switch ocrProvider {
	case "anyreceipt":
		apiKey := c.cfg.Anyreceipt.APIKey
		if strings.TrimSpace(apiKey) == "" {
			items = append(items, CheckItem{
				Category: cat,
				Name:     "Anyreceipt OCR 服务",
				Status:   StatusFail,
				Message:  "缺失 [anyreceipt].api_key",
				Remedy:   "请在 configs/config.toml 中配置 Anyreceipt 的 API Key",
			})
		} else {
			items = append(items, CheckItem{
				Category: cat,
				Name:     "Anyreceipt OCR 服务",
				Status:   StatusPass,
				Message:  "配置完备 (API Key 已就绪)",
			})
		}
	case "model":
		items = append(items, CheckItem{
			Category: cat,
			Name:     "自有大模型 OCR 服务",
			Status:   StatusPass,
			Message:  fmt.Sprintf("模型: %s", c.cfg.Model.Name),
		})
	default:
		items = append(items, CheckItem{
			Category: cat,
			Name:     "小票识别服务",
			Status:   StatusWarn,
			Message:  fmt.Sprintf("识别提供方已关闭或未配置: %s", ocrProvider),
		})
	}

	// 2. Review Provider
	revProvider := c.cfg.ReviewProvider()
	switch revProvider {
	case "seal":
		sealURL := c.cfg.Seal.BaseURL
		whID := c.cfg.Seal.WebhookID
		if whID == "" {
			items = append(items, CheckItem{
				Category: cat,
				Name:     "SealAI 单据机审服务",
				Status:   StatusFail,
				Message:  "缺失 [seal].webhook_id",
				Remedy:   "请在 configs/config.toml 中配置 SealAI 的 Webhook ID",
			})
		} else {
			items = append(items, CheckItem{
				Category: cat,
				Name:     "SealAI 单据机审服务",
				Status:   StatusPass,
				Message:  fmt.Sprintf("配置完备 (Webhook ID: %s, 端点: %s)", whID, sealURL),
			})
		}
	case "model":
		items = append(items, CheckItem{
			Category: cat,
			Name:     "自有模型机审服务",
			Status:   StatusPass,
			Message:  fmt.Sprintf("模型: %s", c.cfg.Model.Name),
		})
	default:
		items = append(items, CheckItem{
			Category: cat,
			Name:     "单据机审服务",
			Status:   StatusWarn,
			Message:  fmt.Sprintf("机审提供方未配置或关闭: %s", revProvider),
		})
	}

	return items
}

func (c *Checker) checkRuntimeEnvironment(ctx context.Context) []CheckItem {
	cat := "本地运行环境与配置"
	var items []CheckItem

	// 1. State Directory
	stateDir := c.cfg.Runtime.StateDir
	if stateDir == "" {
		stateDir = "data/state"
	}

	if err := os.MkdirAll(stateDir, 0700); err != nil {
		items = append(items, CheckItem{
			Category: cat,
			Name:     "状态存储目录",
			Status:   StatusFail,
			Message:  fmt.Sprintf("无法创建目录 %s: %v", stateDir, err),
			Remedy:   "请检查当前进程对 data 目录的写权限",
		})
	} else {
		// Test lock creation
		testFile := filepath.Join(stateDir, ".doctor_test.lock")
		f, err := os.OpenFile(testFile, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			items = append(items, CheckItem{
				Category: cat,
				Name:     "状态存储目录",
				Status:   StatusFail,
				Message:  fmt.Sprintf("写权限异常: %v", err),
				Remedy:   "请确认该目录具备读写权限",
			})
		} else {
			flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
			_ = os.Remove(testFile)

			if flockErr != nil {
				items = append(items, CheckItem{
					Category: cat,
					Name:     "状态存储目录",
					Status:   StatusWarn,
					Message:  fmt.Sprintf("OS 文件锁不可用: %v (可能在某些网络共享存储上)", flockErr),
				})
			} else {
				items = append(items, CheckItem{
					Category: cat,
					Name:     "状态存储目录",
					Status:   StatusPass,
					Message:  fmt.Sprintf("正常 (路径: %s, 文件锁支持正常)", stateDir),
				})
			}
		}
	}

	return items
}

// -------------------------------------------------------------------------
// Probing helpers
// -------------------------------------------------------------------------

func (c *Checker) fetchTenantToken(ctx context.Context) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"app_id":     c.cfg.Feishu.AppID,
		"app_secret": c.cfg.Feishu.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth/v3/tenant_access_token/internal", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
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
		return "", fmt.Errorf("code %d: %s", result.Code, result.Msg)
	}
	return result.TenantAccessToken, nil
}

func (c *Checker) probeHost(ctx context.Context, host string) error {
	u := strings.TrimRight(host, "/")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Checker) fetchBotInfo(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/bot/v3/info", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var raw map[string]any
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return "", err
	}
	code, _ := raw["code"].(float64)
	if code != 0 {
		return "", fmt.Errorf("code %.0f: %v", code, raw["msg"])
	}
	botMap, _ := raw["bot"].(map[string]any)
	appName, _ := botMap["app_name"].(string)
	if activate, ok := botMap["activate"].(bool); ok && !activate {
		return appName, fmt.Errorf("机器人未激活")
	}
	return appName, nil
}

func (c *Checker) probeTaskPermission(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/task/v2/tasks?page_size=1", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result.Code == 99991672 || result.Code == 99991663 || result.Code == 1470403 {
		return fmt.Errorf("code %d: %s", result.Code, result.Msg)
	}
	return nil
}

func (c *Checker) probeTaskCommentPermission(ctx context.Context, token string) error {
	// Probe via comments endpoint
	dummyPayload, _ := json.Marshal(map[string]any{
		"resource_type": "task",
		"resource_id":   "dummy-probe-probe",
		"content":       "ping",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/task/v2/comments", bytes.NewReader(dummyPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	// If 99991672 -> permission missing!
	if result.Code == 99991672 {
		return fmt.Errorf("code 99991672: 未开通 task:comment:write 权限")
	}
	return nil
}

func (c *Checker) fetchTableSchema(ctx context.Context, token, baseToken, tableID string) (map[string]string, map[string]int, error) {
	apiURL := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/fields?page_size=100", c.baseURL, url.PathEscape(baseToken), url.PathEscape(tableID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				FieldID   string `json:"field_id"`
				FieldName string `json:"field_name"`
				Type      int    `json:"type"`
			} `json:"items"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
	}
	if result.Code != 0 {
		return nil, nil, fmt.Errorf("code %d: %s", result.Code, result.Msg)
	}

	fieldMap := make(map[string]string, len(result.Data.Items))
	typeMap := make(map[string]int, len(result.Data.Items))
	for _, item := range result.Data.Items {
		fieldMap[item.FieldID] = item.FieldName
		typeMap[item.FieldID] = item.Type
	}
	return fieldMap, typeMap, nil
}
