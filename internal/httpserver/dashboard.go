package httpserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AnyreceiptStatus reports remaining credits and quota from Anyreceipt OCR service.
type AnyreceiptStatus struct {
	Configured         bool   `json:"configured"`
	BalancePoints      int    `json:"balance_points"`
	AvailableCallCount int    `json:"available_call_count"`
	CallCostPoints     int    `json:"call_cost_points"`
	Error              string `json:"error,omitempty"`
}

// CSTZone is the UTC+8 Beijing timezone used across monitoring dashboards.
var CSTZone = time.FixedZone("Asia/Shanghai", 8*3600)

// FormatCST formats a timestamp into human-readable UTC+8 Beijing time string.
func FormatCST(t time.Time) string {
	if t.IsZero() {
		return "未记录"
	}
	return t.In(CSTZone).Format("2006-01-02 15:04:05 (UTC+8)")
}

// Status contains live runtime and diagnostics information for health dashboards.
type Status struct {
	Version          string            `json:"version"`
	GitCommit        string            `json:"git_commit,omitempty"`
	BuildDate        string            `json:"build_date,omitempty"`
	HTTPAddr         string            `json:"http_addr"`
	StartTime        time.Time         `json:"start_time"`
	Uptime           string            `json:"uptime"`
	UptimeSeconds    int64             `json:"uptime_seconds"`
	FeishuState      string            `json:"feishu_state"` // "ready", "reconnecting", "disconnected", "disabled"
	FeishuEvents     int64             `json:"feishu_events"`
	FeishuConnected  string            `json:"feishu_connected,omitempty"`
	FeishuLastEvent  string            `json:"feishu_last_event,omitempty"`
	FeishuEventTypes []string          `json:"feishu_event_types,omitempty"`
	ReceiptProvider  string            `json:"receipt_provider"`
	ReviewProvider   string            `json:"review_provider"`
	ReviewTrigger    string            `json:"review_trigger"`
	BaseToken        string            `json:"base_token,omitempty"`
	BaseURL          string            `json:"base_url,omitempty"`
	Tables           map[string]string `json:"tables,omitempty"`
	Anyreceipt       *AnyreceiptStatus `json:"anyreceipt,omitempty"`
}

// StatusProvider produces a current status snapshot.
type StatusProvider func() Status

func (s *Server) renderLockScreen(w http.ResponseWriter, _ *http.Request, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	errHTML := ""
	if errMsg != "" {
		errHTML = fmt.Sprintf(`<div class="error-msg">⚠️ %s</div>`, errMsg)
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>TWC 监控看板 · 访问验证</title>
  <style>
    :root {
      --bg: #0f172a;
      --card-bg: #1e293b;
      --border: #334155;
      --text: #f8fafc;
      --text-muted: #94a3b8;
      --accent: #38bdf8;
      --danger: #ef4444;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      background: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", sans-serif;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 1.5rem;
    }
    .lock-card {
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 1rem;
      padding: 2.25rem 2rem;
      width: 100%%;
      max-width: 380px;
      box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.3), 0 8px 10px -6px rgba(0, 0, 0, 0.3);
      text-align: center;
    }
    .icon {
      font-size: 2.5rem;
      margin-bottom: 0.75rem;
      display: inline-block;
    }
    h1 {
      font-size: 1.25rem;
      font-weight: 700;
      color: #fff;
      margin-bottom: 0.35rem;
    }
    p {
      font-size: 0.875rem;
      color: var(--text-muted);
      margin-bottom: 1.5rem;
    }
    .input-group {
      margin-bottom: 1.25rem;
      text-align: left;
    }
    input[type="password"] {
      width: 100%%;
      padding: 0.75rem 1rem;
      background: rgba(15, 23, 42, 0.8);
      border: 1px solid var(--border);
      border-radius: 0.5rem;
      color: #fff;
      font-size: 0.9375rem;
      outline: none;
      transition: border-color 0.15s ease;
    }
    input[type="password"]:focus {
      border-color: var(--accent);
      box-shadow: 0 0 0 3px rgba(56, 189, 248, 0.2);
    }
    button {
      width: 100%%;
      padding: 0.75rem;
      background: #0284c7;
      color: #fff;
      border: none;
      border-radius: 0.5rem;
      font-size: 0.9375rem;
      font-weight: 600;
      cursor: pointer;
      transition: background 0.15s ease;
    }
    button:hover {
      background: #0369a1;
    }
    .error-msg {
      background: rgba(239, 68, 68, 0.15);
      border: 1px solid rgba(239, 68, 68, 0.3);
      color: var(--danger);
      padding: 0.5rem 0.75rem;
      border-radius: 0.5rem;
      font-size: 0.8125rem;
      margin-bottom: 1rem;
    }
  </style>
</head>
<body>
  <div class="lock-card">
    <div class="icon">🔒</div>
    <h1>监控看板验证</h1>
    <p>请输入管理访问密码以解锁看板</p>
    %s
    <form action="/login" method="POST">
      <div class="input-group">
        <input type="password" name="password" placeholder="请输入密码..." autofocus required autocomplete="current-password" />
      </div>
      <button type="submit">解锁并进入 ➔</button>
    </form>
  </div>
</body>
</html>`, errHTML)
	_, _ = w.Write([]byte(html))
}

func (s *Server) renderDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFound(w, r)
		return
	}

	// 1. Process Magic Token from URL parameter ?token=...
	if magicToken := strings.TrimSpace(r.URL.Query().Get("token")); magicToken != "" {
		if s.tokenManager != nil {
			if entry, remaining, ok := s.tokenManager.Validate(magicToken); ok {
				maxAge := int(remaining.Seconds())
				if maxAge <= 0 {
					maxAge = 7200
				}
				http.SetCookie(w, &http.Cookie{
					Name:     "twc_dash_token",
					Value:    entry.Token,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
					MaxAge:   maxAge,
				})
				// Redirect to clean root "/" (removes sensitive token from address bar and referrer)
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			s.renderLockScreen(w, r, "授权访问链接已失效或已过期（有效时长 2 小时），请在飞书中私信发送【管理页面】获取新链接")
			return
		}
	}

	if !s.isAuthenticated(r) {
		s.renderLockScreen(w, r, "")
		return
	}

	var status Status
	if s.statusProvider != nil {
		status = s.statusProvider()
	} else {
		uptime := time.Since(s.startTime).Truncate(time.Second)
		status = Status{
			Version:       s.version,
			HTTPAddr:      s.httpServer.Addr,
			StartTime:     s.startTime,
			Uptime:        uptime.String(),
			UptimeSeconds: int64(uptime.Seconds()),
			FeishuState:   "disabled",
		}
	}

	// If request explicitly wants JSON, return JSON
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") || r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, status)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	feishuBadgeColor := "#10b981" // green
	feishuBadgeText := "已连接 (READY)"
	switch status.FeishuState {
	case "ready":
		feishuBadgeColor = "#10b981"
		feishuBadgeText = "已连接 (READY)"
	case "reconnecting":
		feishuBadgeColor = "#f59e0b"
		feishuBadgeText = "重新连接中 (RECONNECTING)"
	case "disconnected":
		feishuBadgeColor = "#ef4444"
		feishuBadgeText = "已断开 (DISCONNECTED)"
	case "disabled":
		feishuBadgeColor = "#6b7280"
		feishuBadgeText = "未启用 (DISABLED)"
	default:
		feishuBadgeColor = "#3b82f6"
		feishuBadgeText = status.FeishuState
	}

	lastEventText := "暂无事件"
	if status.FeishuLastEvent != "" {
		lastEventText = status.FeishuLastEvent
	}

	connectedTimeText := "未记录"
	if status.FeishuConnected != "" {
		connectedTimeText = status.FeishuConnected
	}

	eventTypesHTML := `<span class="val code">drive.file.bitable_record_changed_v1</span>`
	if len(status.FeishuEventTypes) > 0 {
		var b strings.Builder
		for _, et := range status.FeishuEventTypes {
			b.WriteString(fmt.Sprintf(`<span class="val code">%s</span>`, et))
		}
		eventTypesHTML = b.String()
	}

	baseDisplay := "未绑定 Base"
	if status.BaseToken != "" {
		if status.BaseURL != "" {
			baseDisplay = fmt.Sprintf(`<a href="%s" target="_blank" class="code-link">%s ↗</a>`, status.BaseURL, status.BaseToken)
		} else {
			baseDisplay = fmt.Sprintf(`<span class="code">%s</span>`, status.BaseToken)
		}
	}

	tablesHTML := `<div class="sub-row"><span class="label">无表格绑定</span></div>`
	if len(status.Tables) > 0 {
		var b strings.Builder
		for name, tid := range status.Tables {
			b.WriteString(fmt.Sprintf(`<div class="sub-row"><span class="label">%s</span><span class="val code">%s</span></div>`, name, tid))
		}
		tablesHTML = b.String()
	}

	// Anyreceipt card info
	anyreceiptTagStyle := "background: rgba(107, 114, 128, 0.2); color: #9ca3af; border: 1px solid #4b5563;"
	anyreceiptTagText := "未配置"
	anyreceiptBalanceText := "—"
	anyreceiptCallsText := "—"
	anyreceiptCostText := "1 点 / 次"
	anyreceiptChannelText := "未启用"

	if status.Anyreceipt != nil && status.Anyreceipt.Configured {
		if status.Anyreceipt.Error != "" {
			anyreceiptTagStyle = "background: rgba(239, 68, 68, 0.2); color: #ef4444; border: 1px solid #ef4444;"
			anyreceiptTagText = "查询异常"
			anyreceiptChannelText = fmt.Sprintf("<span style='color: #ef4444;'>%s</span>", status.Anyreceipt.Error)
		} else {
			anyreceiptBalanceText = fmt.Sprintf("%d 点", status.Anyreceipt.BalancePoints)
			anyreceiptCallsText = fmt.Sprintf("%d 次", status.Anyreceipt.AvailableCallCount)
			if status.Anyreceipt.CallCostPoints > 0 {
				anyreceiptCostText = fmt.Sprintf("%d 点 / 次", status.Anyreceipt.CallCostPoints)
			}
			if status.Anyreceipt.AvailableCallCount > 20 {
				anyreceiptTagStyle = "background: rgba(16, 185, 129, 0.2); color: #10b981; border: 1px solid #10b981;"
				anyreceiptTagText = "余额充足"
				anyreceiptChannelText = "<span style='color: #10b981;'>正常运行中</span>"
			} else {
				anyreceiptTagStyle = "background: rgba(245, 158, 11, 0.2); color: #f59e0b; border: 1px solid #f59e0b;"
				anyreceiptTagText = "余额偏低"
				anyreceiptChannelText = "<span style='color: #f59e0b;'>请及时充值</span>"
			}
		}
	}

	versionDisplay := status.Version
	if status.GitCommit != "" {
		versionDisplay = fmt.Sprintf("%s (%s)", status.Version, status.GitCommit)
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>海外易商卡报销网桥 · 监控看板</title>
  <style>
    :root {
      --bg: #0f172a;
      --card-bg: #1e293b;
      --border: #334155;
      --text: #f8fafc;
      --text-muted: #94a3b8;
      --accent: #38bdf8;
      --success: #10b981;
      --warning: #f59e0b;
      --danger: #ef4444;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      background: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", sans-serif;
      padding: 2rem;
      min-height: 100vh;
    }
    .container { max-width: 960px; margin: 0 auto; }
    header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 2rem;
      padding-bottom: 1.5rem;
      border-bottom: 1px solid var(--border);
    }
    .title-group h1 { font-size: 1.5rem; font-weight: 700; color: #fff; display: flex; align-items: center; gap: 0.6rem; }
    .title-group p { font-size: 0.875rem; color: var(--text-muted); margin-top: 0.25rem; }
    .header-actions {
      display: flex;
      align-items: center;
      gap: 0.75rem;
    }
    .live-badge {
      display: inline-flex;
      align-items: center;
      gap: 0.5rem;
      padding: 0.4rem 0.85rem;
      border-radius: 9999px;
      font-size: 0.8125rem;
      font-weight: 600;
      background: rgba(16, 185, 129, 0.15);
      border: 1px solid rgba(16, 185, 129, 0.3);
      color: var(--success);
    }
    .pulse-dot {
      width: 8px;
      height: 8px;
      border-radius: 50%%;
      background: var(--success);
      box-shadow: 0 0 10px var(--success);
      animation: pulse 2s infinite;
    }
    @keyframes pulse {
      0%% { transform: scale(0.95); opacity: 0.8; }
      50%% { transform: scale(1.2); opacity: 1; }
      100%% { transform: scale(0.95); opacity: 0.8; }
    }
    .logout-btn {
      color: var(--text-muted);
      font-size: 0.8125rem;
      text-decoration: none;
      padding: 0.35rem 0.75rem;
      border: 1px solid var(--border);
      border-radius: 0.375rem;
      transition: all 0.15s ease;
    }
    .logout-btn:hover {
      color: #fff;
      border-color: #64748b;
      background: rgba(255,255,255,0.05);
    }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(440px, 1fr)); gap: 1.25rem; }
    .card {
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 0.75rem;
      padding: 1.5rem;
      box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1);
    }
    .card-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 1.25rem;
      padding-bottom: 0.75rem;
      border-bottom: 1px solid rgba(255,255,255,0.06);
    }
    .card-title { font-size: 1rem; font-weight: 600; color: #fff; display: flex; align-items: center; gap: 0.5rem; }
    .status-tag {
      font-size: 0.75rem;
      font-weight: 600;
      padding: 0.25rem 0.6rem;
      border-radius: 0.375rem;
    }
    .row { display: flex; justify-content: space-between; align-items: center; padding: 0.5rem 0; font-size: 0.875rem; }
    .label { color: var(--text-muted); }
    .val { font-weight: 500; color: #f1f5f9; }
    .code { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; background: rgba(0,0,0,0.25); padding: 0.15rem 0.4rem; border-radius: 0.25rem; font-size: 0.8125rem; }
    .code-link { color: var(--accent); text-decoration: none; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
    .code-link:hover { text-decoration: underline; }
    .sub-row { display: flex; justify-content: space-between; padding: 0.35rem 0; font-size: 0.8125rem; border-top: 1px dashed rgba(255,255,255,0.05); }

    /* 顶部导航 Tab */
    .nav-tabs {
      display: flex;
      gap: 0.5rem;
      margin-bottom: 1.5rem;
      border-bottom: 1px solid var(--border);
      padding-bottom: 0.75rem;
      overflow-x: auto;
    }
    .nav-tab {
      background: transparent;
      color: var(--text-muted);
      border: 1px solid transparent;
      padding: 0.5rem 1rem;
      border-radius: 0.5rem;
      font-size: 0.875rem;
      font-weight: 600;
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 0.5rem;
      transition: all 0.2s ease;
      white-space: nowrap;
    }
    .nav-tab:hover {
      color: #fff;
      background: rgba(255, 255, 255, 0.05);
    }
    .nav-tab.active {
      color: #fff;
      background: rgba(56, 189, 248, 0.15);
      border-color: rgba(56, 189, 248, 0.4);
      box-shadow: 0 0 12px rgba(56, 189, 248, 0.15);
    }
    .tab-badge {
      font-size: 0.7rem;
      padding: 0.15rem 0.45rem;
      border-radius: 9999px;
      background: rgba(255, 255, 255, 0.1);
      color: var(--text-muted);
    }
    .nav-tab.active .tab-badge {
      background: rgba(56, 189, 248, 0.3);
      color: #38bdf8;
    }
    .tab-badge.live {
      background: rgba(16, 185, 129, 0.2);
      color: #10b981;
    }
    .quick-banner {
      margin-top: 1.25rem;
      padding: 1rem 1.25rem;
      background: rgba(30, 41, 59, 0.7);
      border: 1px dashed var(--border);
      border-radius: 0.75rem;
      display: flex;
      justify-content: space-between;
      align-items: center;
      flex-wrap: wrap;
      gap: 0.75rem;
    }
    .quick-banner .text { font-size: 0.875rem; color: var(--text-muted); }
    .quick-banner .actions { display: flex; gap: 0.5rem; }
    .btn-action {
      background: rgba(56, 189, 248, 0.12);
      color: var(--accent);
      border: 1px solid rgba(56, 189, 248, 0.3);
      padding: 0.35rem 0.75rem;
      border-radius: 0.375rem;
      font-size: 0.8125rem;
      font-weight: 600;
      cursor: pointer;
      text-decoration: none;
      transition: all 0.15s;
    }
    .btn-action:hover {
      background: var(--accent);
      color: #0f172a;
    }
    .console-box {
      background: #090d16;
      border: 1px solid var(--border);
      border-radius: 0.5rem;
      padding: 1rem;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      font-size: 0.8125rem;
      height: 520px;
      overflow-y: auto;
      line-height: 1.6;
      white-space: pre-wrap;
      word-break: break-all;
    }
    .console-line {
      padding: 0.15rem 0;
      display: flex;
      gap: 0.6rem;
      border-bottom: 1px solid rgba(255, 255, 255, 0.03);
    }
    .console-time { color: #64748b; flex-shrink: 0; font-size: 0.75rem; }
    .console-level { font-weight: 600; flex-shrink: 0; padding: 0 0.35rem; border-radius: 3px; font-size: 0.7rem; line-height: 1.5; height: fit-content; }
    .level-info { color: #38bdf8; background: rgba(56, 189, 248, 0.15); border: 1px solid rgba(56, 189, 248, 0.3); }
    .level-warn { color: #f59e0b; background: rgba(245, 158, 11, 0.15); border: 1px solid rgba(245, 158, 11, 0.3); }
    .level-error { color: #ef4444; background: rgba(239, 68, 68, 0.15); border: 1px solid rgba(239, 68, 68, 0.3); }
    .level-debug { color: #94a3b8; background: rgba(148, 163, 184, 0.15); border: 1px solid rgba(148, 163, 184, 0.3); }
    .console-msg { color: #f1f5f9; flex-grow: 1; }
    .console-attrs { color: #94a3b8; font-size: 0.75rem; margin-left: 0.25rem; }
    .toolbar {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 1rem;
      flex-wrap: wrap;
      gap: 0.75rem;
    }
    .btn-group { display: flex; gap: 0.25rem; }
    .btn-filter {
      background: rgba(255,255,255,0.06);
      border: 1px solid var(--border);
      color: var(--text-muted);
      padding: 0.3rem 0.65rem;
      border-radius: 0.375rem;
      font-size: 0.75rem;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.15s;
    }
    .btn-filter:hover { color: #fff; background: rgba(255,255,255,0.1); }
    .btn-filter.active { background: var(--accent); color: #0f172a; border-color: var(--accent); }
    .search-input {
      background: rgba(15,23,42,0.8);
      border: 1px solid var(--border);
      color: #f8fafc;
      padding: 0.35rem 0.75rem;
      border-radius: 6px;
      font-size: 0.8125rem;
      outline: none;
    }
    .search-input:focus { border-color: var(--accent); }

    footer {
      margin-top: 2rem;
      text-align: center;
      font-size: 0.8125rem;
      color: var(--text-muted);
      display: flex;
      justify-content: space-between;
      align-items: center;
    }
    .links a { color: var(--accent); text-decoration: none; margin-left: 1rem; }
    .links a:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <div class="container">
    <header>
      <div class="title-group">
        <h1><span>⚡</span> 海外易商卡报销网桥</h1>
        <p>TWC Approval Go Bridge · 实时运行状态与诊断监控</p>
      </div>
      <div class="header-actions">
        <div class="live-badge" id="system-badge">
          <span class="pulse-dot"></span>
          <span id="system-status-text">服务正常运行</span>
        </div>
        <a href="/logout" class="logout-btn">退出登录</a>
      </div>
    </header>

    <!-- 顶部导航 Tab -->
    <nav class="nav-tabs">
      <button class="nav-tab active" id="tab-btn-overview" onclick="switchTab('overview')">
        <span>📊</span> 监控概览
      </button>
      <button class="nav-tab" id="tab-btn-audit" onclick="switchTab('audit')">
        <span>📜</span> 业务单据审计流水
        <span class="tab-badge" id="audit-badge">0 条</span>
      </button>
      <button class="nav-tab" id="tab-btn-system-logs" onclick="switchTab('system-logs')">
        <span>💻</span> 服务运行终端日志
        <span class="tab-badge live" id="logs-badge">● 实时</span>
      </button>
    </nav>

    <!-- Tab 1: 监控概览 -->
    <div id="view-overview" class="tab-pane">
      <div class="grid">
        <!-- 飞书长连接卡片 -->
      <div class="card">
        <div class="card-header">
          <div class="card-title"><span>📡</span> 飞书 WebSocket 长连接</div>
          <span class="status-tag" id="feishu-tag" style="background: rgba(%s, 0.2); color: %s; border: 1px solid %s;">%s</span>
        </div>
        <div class="row">
          <span class="label">连接建立时间</span>
          <span class="val" id="feishu-connected-at">%s</span>
        </div>
        <div class="row">
          <span class="label">累计接收事件数</span>
          <span class="val code" id="feishu-event-count">%d</span>
        </div>
        <div class="row">
          <span class="label">最近一次事件</span>
          <span class="val" id="feishu-last-event">%s</span>
        </div>
        <div class="row" style="align-items: flex-start;">
          <span class="label">监听事件类型</span>
          <div id="feishu-event-types" style="display: flex; flex-direction: column; align-items: flex-end; gap: 0.25rem;">
            %s
          </div>
        </div>
      </div>

      <!-- Anyreceipt 票据识别额度卡片 -->
      <div class="card">
        <div class="card-header">
          <div class="card-title"><span>🧾</span> Anyreceipt 海外小票识别额度</div>
          <span class="status-tag" id="anyreceipt-tag" style="%s">%s</span>
        </div>
        <div class="row">
          <span class="label">账户剩余余额</span>
          <span class="val" id="anyreceipt-balance" style="font-size: 1.125rem; font-weight: 700; color: #38bdf8;">%s</span>
        </div>
        <div class="row">
          <span class="label">预计可用调用次数</span>
          <span class="val code" id="anyreceipt-calls" style="color: #10b981;">%s</span>
        </div>
        <div class="row">
          <span class="label">单次识别计费标准</span>
          <span class="val">%s</span>
        </div>
        <div class="row">
          <span class="label">通道状态</span>
          <span class="val" id="anyreceipt-channel">%s</span>
        </div>
      </div>

      <!-- 服务基础信息 -->
      <div class="card">
        <div class="card-header">
          <div class="card-title"><span>⚙️</span> 基础服务指标</div>
          <span class="status-tag" style="background: rgba(56, 189, 248, 0.15); color: var(--accent); border: 1px solid rgba(56, 189, 248, 0.3);">HTTP SERVER</span>
        </div>
        <div class="row">
          <span class="label">监听端口 / 地址</span>
          <span class="val code">%s</span>
        </div>
        <div class="row">
          <span class="label">服务版本 (Version)</span>
          <span class="val code">%s</span>
        </div>
        <div class="row">
          <span class="label">已稳定运行 (Uptime)</span>
          <span class="val" id="system-uptime">%s</span>
        </div>
        <div class="row">
          <span class="label">启动时间</span>
          <span class="val">%s</span>
        </div>
      </div>

      <!-- 多维表格拓扑 -->
      <div class="card">
        <div class="card-header">
          <div class="card-title"><span>📊</span> 业务多维表格绑定拓扑</div>
        </div>
        <div class="row">
          <span class="label">Base Token</span>
          <span class="val">%s</span>
        </div>
        <div style="margin-top: 0.5rem; padding-top: 0.5rem; border-top: 1px solid rgba(255,255,255,0.06);">
          %s
        </div>
      </div>

      <!-- AI 与票据处理引擎 -->
      <div class="card">
        <div class="card-header">
          <div class="card-title"><span>🤖</span> 识别与单据机审引擎</div>
        </div>
        <div class="row">
          <span class="label">海外小票 OCR 识别</span>
          <span class="val code">%s</span>
        </div>
        <div class="row">
          <span class="label">单据 AI 机审通道</span>
          <span class="val code">%s</span>
        </div>
        <div class="row">
          <span class="label">机审触发模式</span>
          <span class="val code">%s</span>
        </div>
        <div class="row">
          <span class="label">交易流水联动读取</span>
          <span class="val" style="color: var(--success);">已启用 (只读比对)</span>
        </div>
      </div>
      </div>
      <!-- 底部快速排查引导 -->
      <div class="quick-banner">
        <div class="text">
          💡 <strong>快速切换：</strong>需要查看单据生命周期演进轨迹或后台终端日志输出？
        </div>
        <div class="actions">
          <button class="btn-action" onclick="switchTab('audit')">📜 查看业务单据审计流水 →</button>
          <button class="btn-action" onclick="switchTab('system-logs')">💻 打开服务运行实时日志 →</button>
        </div>
      </div>
    </div>

    <!-- Tab 2: 业务单据审计流水 -->
    <div id="view-audit" class="tab-pane" style="display: none;">
      <div class="card">
        <div class="card-header">
          <div class="card-title"><span>📜</span> 业务运行时操作审计与单据生命周期追溯</div>
          <div style="display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap;">
            <select id="audit-filter-type" class="search-input" onchange="filterAuditTable()" style="cursor: pointer;">
              <option value="">全部事件分类</option>
              <option value="notify_card_sent">📱 推送卡片</option>
              <option value="file_uploaded">📎 文件上传</option>
              <option value="ocr_initiated">🔍 发起OCR</option>
              <option value="ocr_completed">✅ OCR完成</option>
              <option value="ocr_failed">❌ OCR失败</option>
              <option value="review_submitted">⚖️ 发起审核</option>
              <option value="review_completed">🎯 机审完成</option>
              <option value="review_writeback">📝 结果回写</option>
              <option value="dead_letter_alert">🔴 死信告警</option>
            </select>
            <input type="text" id="audit-search-id" class="search-input" placeholder="输入记录ID (如 reczz28...)" style="width: 220px;" onkeypress="if(event.key==='Enter') queryTimeline();">
            <button onclick="queryTimeline()" class="btn-action" style="background: var(--accent); color: #0f172a; border-color: var(--accent);">查时间线</button>
            <button onclick="updateAudits()" class="toolbar-btn">刷新</button>
          </div>
        </div>
        <div id="timeline-box" style="display: none; margin-bottom: 1rem; padding: 1rem; background: rgba(15,23,42,0.8); border-radius: 8px; border: 1px solid rgba(56,189,248,0.3);">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.5rem;">
            <span style="font-weight: 600; color: var(--accent);" id="timeline-title">单据全生命周期时间线</span>
            <button onclick="closeTimeline()" style="background: transparent; border: none; color: var(--text-muted); cursor: pointer;">✕ 关闭</button>
          </div>
          <div id="timeline-content" style="font-family: monospace; font-size: 0.85rem; line-height: 1.6; white-space: pre-wrap;"></div>
        </div>
        <div style="overflow-x: auto;">
          <table style="width: 100%%; border-collapse: collapse; font-size: 0.85rem; text-align: left;">
            <thead>
              <tr style="border-bottom: 1px solid var(--border); color: var(--text-muted);">
                <th style="padding: 0.6rem 0.5rem;">发生时间</th>
                <th style="padding: 0.6rem 0.5rem;">事件分类</th>
                <th style="padding: 0.6rem 0.5rem;">单据记录ID</th>
                <th style="padding: 0.6rem 0.5rem;">人员</th>
                <th style="padding: 0.6rem 0.5rem;">动作与元数据</th>
              </tr>
            </thead>
            <tbody id="audit-table-body">
              <tr><td colspan="5" style="padding: 1.5rem; text-align: center; color: var(--text-muted);">加载中...</td></tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Tab 3: 服务运行实时日志 -->
    <div id="view-system-logs" class="tab-pane" style="display: none;">
      <div class="card">
        <div class="card-header">
          <div class="card-title"><span>💻</span> 服务后台实时运行日志 (Terminal Console)</div>
          <div style="display: flex; align-items: center; gap: 0.6rem;">
            <span class="tab-badge live" id="log-polling-status">● 实时监听中</span>
            <span class="val code" id="log-count-display">0 条</span>
          </div>
        </div>

        <div class="toolbar">
          <div style="display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap;">
            <div class="btn-group">
              <button class="btn-filter active" id="filter-btn-all" onclick="setLogLevelFilter('ALL')">全部</button>
              <button class="btn-filter" id="filter-btn-info" onclick="setLogLevelFilter('INFO')">INFO</button>
              <button class="btn-filter" id="filter-btn-warn" onclick="setLogLevelFilter('WARN')">WARN</button>
              <button class="btn-filter" id="filter-btn-error" onclick="setLogLevelFilter('ERROR')">ERROR</button>
              <button class="btn-filter" id="filter-btn-debug" onclick="setLogLevelFilter('DEBUG')">DEBUG</button>
            </div>
            <input type="text" id="log-search-input" class="search-input" placeholder="🔍 搜索关键字..." oninput="filterLogs()" style="width: 200px;">
          </div>
          <div style="display: flex; gap: 0.5rem; align-items: center;">
            <label style="display: inline-flex; align-items: center; gap: 0.35rem; font-size: 0.8125rem; color: var(--text-muted); cursor: pointer; user-select: none;">
              <input type="checkbox" id="auto-scroll-toggle" checked> 自动滚动
            </label>
            <button onclick="toggleLogPause()" id="pause-logs-btn" class="toolbar-btn">暂停</button>
            <button onclick="clearConsole()" class="toolbar-btn">清屏</button>
            <button onclick="refreshLogsFull()" class="toolbar-btn">重新载入</button>
          </div>
        </div>

        <div class="console-box" id="console-output">
          <div style="color: #64748b; text-align: center; padding: 2rem;">正在连接日志流...</div>
        </div>
      </div>
    </div>

    <footer>
      <span>自动无刷新轮询中 · 数据每 3 秒同步一次</span>
      <div class="links">
        <a href="https://admin.doubao.com/ask/doubao/builtin-skill" target="_blank" style="color: #38bdf8; font-weight: 500;">豆包企业技能后台 ↗</a>
        <a href="/api/audit" target="_blank">/api/audit</a>
        <a href="/api/status" target="_blank">/api/status</a>
        <a href="/healthz" target="_blank">/healthz</a>
        <a href="/readyz" target="_blank">/readyz</a>
        <a href="/version" target="_blank">/version</a>
      </div>
    </footer>
  </div>

  <script>
    async function updateStatus() {
      try {
        const res = await fetch('/api/status');
        if (!res.ok) {
          if (res.status === 401) {
            window.location.reload();
            return;
          }
          throw new Error('status error');
        }
        const data = await res.json();
        
        // Update Feishu Tag
        const tag = document.getElementById('feishu-tag');
        if (data.feishu_state === 'ready') {
          tag.textContent = '已连接 (READY)';
          tag.style.background = 'rgba(16, 185, 129, 0.2)';
          tag.style.color = '#10b981';
          tag.style.borderColor = '#10b981';
        } else if (data.feishu_state === 'reconnecting') {
          tag.textContent = '重新连接中 (RECONNECTING)';
          tag.style.background = 'rgba(245, 158, 11, 0.2)';
          tag.style.color = '#f59e0b';
          tag.style.borderColor = '#f59e0b';
        } else if (data.feishu_state === 'disconnected') {
          tag.textContent = '已断开 (DISCONNECTED)';
          tag.style.background = 'rgba(239, 68, 68, 0.2)';
          tag.style.color = '#ef4444';
          tag.style.borderColor = '#ef4444';
        }

        if (data.feishu_events !== undefined) {
          document.getElementById('feishu-event-count').textContent = data.feishu_events;
        }
        if (data.feishu_connected) {
          const connEl = document.getElementById('feishu-connected-at');
          if (connEl) connEl.textContent = data.feishu_connected;
        }
        if (data.feishu_last_event) {
          document.getElementById('feishu-last-event').textContent = data.feishu_last_event;
        }
        if (data.feishu_event_types && data.feishu_event_types.length > 0) {
          const typesContainer = document.getElementById('feishu-event-types');
          if (typesContainer) {
            typesContainer.innerHTML = data.feishu_event_types.map(t => '<span class="val code">' + escapeHtml(t) + '</span>').join('');
          }
        }
        if (data.uptime) {
          document.getElementById('system-uptime').textContent = data.uptime;
        }

        // Update Anyreceipt Tag
        if (data.anyreceipt && data.anyreceipt.configured) {
          const atag = document.getElementById('anyreceipt-tag');
          if (data.anyreceipt.error) {
            atag.textContent = '查询异常';
            atag.style.background = 'rgba(239, 68, 68, 0.2)';
            atag.style.color = '#ef4444';
            atag.style.borderColor = '#ef4444';
          } else {
            document.getElementById('anyreceipt-balance').textContent = data.anyreceipt.balance_points + ' 点';
            document.getElementById('anyreceipt-calls').textContent = data.anyreceipt.available_call_count + ' 次';
            if (data.anyreceipt.available_call_count > 20) {
              atag.textContent = '余额充足';
              atag.style.background = 'rgba(16, 185, 129, 0.2)';
              atag.style.color = '#10b981';
              atag.style.borderColor = '#10b981';
            } else {
              atag.textContent = '余额偏低';
              atag.style.background = 'rgba(245, 158, 11, 0.2)';
              atag.style.color = '#f59e0b';
              atag.style.borderColor = '#f59e0b';
            }
          }
        }
      } catch (e) {
        const badge = document.getElementById('system-badge');
        badge.style.background = 'rgba(239, 68, 68, 0.2)';
        badge.style.borderColor = 'rgba(239, 68, 68, 0.5)';
        badge.style.color = '#ef4444';
        document.getElementById('system-status-text').textContent = '服务离线或网络异常';
      }
    }

    let activeTab = 'overview';
    let cachedAudits = [];
    let systemLogs = [];
    let lastLogId = 0;
    let currentLogLevel = 'ALL';
    let logSearchText = '';
    let autoScroll = true;
    let logsPaused = false;

    function switchTab(tabId) {
      if (tabId === 'logs') tabId = 'system-logs';
      activeTab = tabId;
      document.querySelectorAll('.tab-pane').forEach(el => el.style.display = 'none');
      document.querySelectorAll('.nav-tab').forEach(el => el.classList.remove('active'));

      const targetPane = document.getElementById('view-' + tabId);
      const targetBtn = document.getElementById('tab-btn-' + tabId);
      if (targetPane) targetPane.style.display = 'block';
      if (targetBtn) targetBtn.classList.add('active');

      try {
        history.replaceState(null, '', '#' + tabId);
      } catch (e) {}

      if (tabId === 'system-logs') {
        renderConsole();
        pollLogs();
      } else if (tabId === 'audit') {
        updateAudits();
      }
    }

    function initTabFromHash() {
      let hash = window.location.hash.replace('#', '').trim();
      if (hash === 'logs') hash = 'system-logs';
      if (['overview', 'audit', 'system-logs'].includes(hash)) {
        switchTab(hash);
      } else {
        switchTab('overview');
      }
    }
    window.addEventListener('hashchange', initTabFromHash);

    async function updateAudits() {
      try {
        const res = await fetch('/api/audit?limit=50');
        if (!res.ok) return;
        const data = await res.json();
        cachedAudits = data.entries || [];
        document.getElementById('audit-badge').textContent = (data.total || cachedAudits.length) + ' 条';
        renderAuditRows();
      } catch (err) {}
    }

    function filterAuditTable() {
      renderAuditRows();
    }

    function renderAuditRows() {
      const tbody = document.getElementById('audit-table-body');
      if (!tbody) return;
      const typeFilter = document.getElementById('audit-filter-type').value;
      const filtered = cachedAudits.filter(e => !typeFilter || e.type === typeFilter);

      if (filtered.length === 0) {
        tbody.innerHTML = '<tr><td colspan="5" style="padding: 1.5rem; text-align: center; color: var(--text-muted);">暂无匹配业务操作日志</td></tr>';
        return;
      }

      let html = '';
      for (const e of filtered) {
        const t = new Date(e.timestamp).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) + ' (UTC+8)';
        let badge = '🔹 ' + e.type;
        if (e.type === 'notify_card_sent') badge = '<span style="color: #38bdf8;">📱 推送卡片</span>';
        else if (e.type === 'file_uploaded') badge = '<span style="color: #a855f7;">📎 文件上传</span>';
        else if (e.type === 'ocr_initiated') badge = '<span style="color: #f59e0b;">🔍 发起OCR</span>';
        else if (e.type === 'ocr_completed') badge = '<span style="color: #10b981;">✅ OCR完成</span>';
        else if (e.type === 'ocr_failed') badge = '<span style="color: #ef4444;">❌ OCR失败</span>';
        else if (e.type === 'review_submitted') badge = '<span style="color: #f97316;">⚖️ 发起审核</span>';
        else if (e.type === 'review_completed') badge = '<span style="color: #10b981;">🎯 机审完成</span>';
        else if (e.type === 'review_writeback') badge = '<span style="color: #38bdf8;">📝 结果回写</span>';
        else if (e.type === 'dead_letter_alert') badge = '<span style="color: #ef4444; font-weight: bold;">🔴 死信告警</span>';

        let actor = e.operator_name || e.operator || '-';
        let details = '';
        if (e.details && Object.keys(e.details).length > 0) {
          details = ' <span style="color: var(--text-muted); font-size: 0.8rem;">(' + Object.entries(e.details).map(([k,v]) => k + ': ' + v).join(', ') + ')</span>';
        }
        html += '<tr style="border-bottom: 1px solid rgba(255,255,255,0.04);">' +
          '<td style="padding: 0.45rem 0.5rem; color: var(--text-muted);">' + t + '</td>' +
          '<td style="padding: 0.45rem 0.5rem;">' + badge + '</td>' +
          '<td style="padding: 0.45rem 0.5rem;"><a href="javascript:void(0)" onclick="quickQuery(\'' + e.record_id + '\')" style="color: var(--accent); text-decoration: none;" class="code">' + e.record_id + '</a></td>' +
          '<td style="padding: 0.45rem 0.5rem;">' + actor + '</td>' +
          '<td style="padding: 0.45rem 0.5rem;">' + e.action + details + '</td>' +
          '</tr>';
      }
      tbody.innerHTML = html;
    }

    async function queryTimeline() {
      const id = document.getElementById('audit-search-id').value.trim();
      if (!id) return;
      const box = document.getElementById('timeline-box');
      const content = document.getElementById('timeline-content');
      document.getElementById('timeline-title').textContent = '单据 [' + id + '] 全生命周期演进时间线';
      box.style.display = 'block';
      content.textContent = '查询中...';

      try {
        const res = await fetch('/api/audit/timeline?record_id=' + encodeURIComponent(id));
        if (!res.ok) {
          content.textContent = '查询失败: ' + res.statusText;
          return;
        }
        const data = await res.json();
        if (!data.timeline || data.timeline.length === 0) {
          content.textContent = '单据 [' + id + '] 暂无业务生命周期日志记录';
          return;
        }
        let txt = '';
        data.timeline.forEach((e, idx) => {
          const isLast = idx === data.timeline.length - 1;
          const prefix = isLast ? '└─ ' : '├─ ';
          const t = new Date(e.timestamp).toLocaleString('zh-CN', { hour12: false });
          let meta = '';
          if (e.details && Object.keys(e.details).length > 0) {
            meta = '\n   ' + (isLast ? '   ' : '│  ') + '元数据: ' + JSON.stringify(e.details);
          }
          txt += prefix + '[' + t + '] 【' + e.type + '】 ' + (e.operator_name || e.operator || '') + ' ' + e.action + meta + '\n';
        });
        content.textContent = txt;
      } catch (err) {
        content.textContent = '网络错误: ' + err.message;
      }
    }

    function quickQuery(id) {
      switchTab('audit');
      document.getElementById('audit-search-id').value = id;
      queryTimeline();
    }

    function closeTimeline() {
      document.getElementById('timeline-box').style.display = 'none';
    }

    // ================= System Terminal Logs =================
    async function pollLogs() {
      if (logsPaused) return;
      try {
        const url = lastLogId > 0 
          ? '/api/system-logs?since_id=' + lastLogId 
          : '/api/system-logs?limit=200';
        const res = await fetch(url);
        if (!res.ok) return;
        const data = await res.json();
        if (data.logs && data.logs.length > 0) {
          if (lastLogId === 0) {
            systemLogs = data.logs;
          } else {
            systemLogs = systemLogs.concat(data.logs);
            if (systemLogs.length > 1000) {
              systemLogs = systemLogs.slice(systemLogs.length - 1000);
            }
          }
          lastLogId = data.last_id || (systemLogs[systemLogs.length - 1] ? systemLogs[systemLogs.length - 1].id : 0);
          renderConsole();
        }
      } catch (e) {}
    }

    function renderConsole() {
      const box = document.getElementById('console-output');
      if (!box) return;

      const filtered = systemLogs.filter(e => {
        if (currentLogLevel !== 'ALL' && e.level !== currentLogLevel) {
          return false;
        }
        if (logSearchText) {
          const raw = (e.message + ' ' + JSON.stringify(e.attrs || {})).toLowerCase();
          if (!raw.includes(logSearchText.toLowerCase())) return false;
        }
        return true;
      });

      document.getElementById('log-count-display').textContent = filtered.length + ' / ' + systemLogs.length + ' 条';

      if (filtered.length === 0) {
        box.innerHTML = '<div style="color: #64748b; text-align: center; padding: 2rem;">' + (systemLogs.length === 0 ? '暂无实时运行日志' : '未匹配到符合条件的日志行') + '</div>';
        return;
      }

      let html = '';
      for (const e of filtered) {
        const t = new Date(e.time).toLocaleTimeString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' }) + '.' + String(new Date(e.time).getMilliseconds()).padStart(3, '0');
        let lvlClass = 'level-info';
        if (e.level === 'WARN') lvlClass = 'level-warn';
        else if (e.level === 'ERROR') lvlClass = 'level-error';
        else if (e.level === 'DEBUG') lvlClass = 'level-debug';

        let attrsStr = '';
        if (e.attrs && Object.keys(e.attrs).length > 0) {
          attrsStr = '<span class="console-attrs">' + Object.entries(e.attrs).map(([k, v]) => k + '=' + (typeof v === 'string' ? '"' + v + '"' : JSON.stringify(v))).join(' ') + '</span>';
        }

        html += '<div class="console-line">' +
          '<span class="console-time">' + t + '</span>' +
          '<span class="console-level ' + lvlClass + '">' + e.level + '</span>' +
          '<span class="console-msg">' + escapeHtml(e.message) + ' ' + attrsStr + '</span>' +
          '</div>';
      }
      box.innerHTML = html;

      if (autoScroll) {
        box.scrollTop = box.scrollHeight;
      }
    }

    function escapeHtml(str) {
      if (!str) return '';
      return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
    }

    function setLogLevelFilter(lvl) {
      currentLogLevel = lvl;
      document.querySelectorAll('.btn-group .btn-filter').forEach(b => b.classList.remove('active'));
      const activeBtn = document.getElementById('filter-btn-' + lvl.toLowerCase());
      if (activeBtn) activeBtn.classList.add('active');
      renderConsole();
    }

    function filterLogs() {
      logSearchText = document.getElementById('log-search-input').value.trim();
      renderConsole();
    }

    function toggleLogPause() {
      logsPaused = !logsPaused;
      const btn = document.getElementById('pause-logs-btn');
      const status = document.getElementById('log-polling-status');
      if (logsPaused) {
        btn.textContent = '继续';
        btn.style.color = '#38bdf8';
        status.textContent = '⏸ 已暂停';
        status.style.color = '#f59e0b';
      } else {
        btn.textContent = '暂停';
        btn.style.color = '#f1f5f9';
        status.textContent = '● 实时监听中';
        status.style.color = '#10b981';
        pollLogs();
      }
    }

    function clearConsole() {
      systemLogs = [];
      renderConsole();
    }

    function refreshLogsFull() {
      lastLogId = 0;
      systemLogs = [];
      pollLogs();
    }

    document.getElementById('auto-scroll-toggle').addEventListener('change', (e) => {
      autoScroll = e.target.checked;
      if (autoScroll) {
        const box = document.getElementById('console-output');
        if (box) box.scrollTop = box.scrollHeight;
      }
    });

    initTabFromHash();
    updateStatus();
    updateAudits();
    pollLogs();

    setInterval(updateStatus, 3000);
    setInterval(updateAudits, 5000);
    setInterval(() => {
      if (activeTab === 'system-logs') {
        pollLogs();
      }
    }, 2000);
  </script>
</body>
</html>`,
		status.FeishuState, feishuBadgeColor, feishuBadgeColor, feishuBadgeText,
		connectedTimeText,
		status.FeishuEvents,
		lastEventText,
		eventTypesHTML,
		anyreceiptTagStyle, anyreceiptTagText,
		anyreceiptBalanceText,
		anyreceiptCallsText,
		anyreceiptCostText,
		anyreceiptChannelText,
		status.HTTPAddr,
		versionDisplay,
		status.Uptime,
		FormatCST(status.StartTime),
		baseDisplay,
		tablesHTML,
		status.ReceiptProvider,
		status.ReviewProvider,
		status.ReviewTrigger,
	)

	_, _ = w.Write([]byte(html))
}
