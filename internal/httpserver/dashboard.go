package httpserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Status contains live runtime and diagnostics information for health dashboards.
type Status struct {
	Version         string            `json:"version"`
	HTTPAddr        string            `json:"http_addr"`
	StartTime       time.Time         `json:"start_time"`
	Uptime          string            `json:"uptime"`
	UptimeSeconds   int64             `json:"uptime_seconds"`
	FeishuState     string            `json:"feishu_state"` // "ready", "reconnecting", "disconnected", "disabled"
	FeishuEvents    int64             `json:"feishu_events"`
	FeishuConnected string            `json:"feishu_connected,omitempty"`
	FeishuLastEvent string            `json:"feishu_last_event,omitempty"`
	ReceiptProvider string            `json:"receipt_provider"`
	ReviewProvider  string            `json:"review_provider"`
	ReviewTrigger   string            `json:"review_trigger"`
	BaseToken       string            `json:"base_token,omitempty"`
	BaseURL         string            `json:"base_url,omitempty"`
	Tables          map[string]string `json:"tables,omitempty"`
}

// StatusProvider produces a current status snapshot.
type StatusProvider func() Status

func (s *Server) renderDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFound(w, r)
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
      <div class="live-badge" id="system-badge">
        <span class="pulse-dot"></span>
        <span id="system-status-text">服务正常运行</span>
      </div>
    </header>

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
        <div class="row">
          <span class="label">监听事件类型</span>
          <span class="val code">drive.file.bitable_record_changed_v1</span>
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

    <footer>
      <span>自动无刷新轮询中 · 数据每 3 秒同步一次</span>
      <div class="links">
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
        if (!res.ok) throw new Error('status error');
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

        if (data.feishu_event_count !== undefined) {
          document.getElementById('feishu-event-count').textContent = data.feishu_events;
        }
        if (data.feishu_last_event) {
          document.getElementById('feishu-last-event').textContent = data.feishu_last_event;
        }
        if (data.uptime) {
          document.getElementById('system-uptime').textContent = data.uptime;
        }
      } catch (e) {
        const badge = document.getElementById('system-badge');
        badge.style.background = 'rgba(239, 68, 68, 0.2)';
        badge.style.borderColor = 'rgba(239, 68, 68, 0.5)';
        badge.style.color = '#ef4444';
        document.getElementById('system-status-text').textContent = '服务离线或网络异常';
      }
    }
    setInterval(updateStatus, 3000);
  </script>
</body>
</html>`,
		status.FeishuState, feishuBadgeColor, feishuBadgeColor, feishuBadgeText,
		connectedTimeText,
		status.FeishuEvents,
		lastEventText,
		status.HTTPAddr,
		status.Version,
		status.Uptime,
		status.StartTime.Format("2006-01-02 15:04:05"),
		baseDisplay,
		tablesHTML,
		status.ReceiptProvider,
		status.ReviewProvider,
		status.ReviewTrigger,
	)

	_, _ = w.Write([]byte(html))
}
