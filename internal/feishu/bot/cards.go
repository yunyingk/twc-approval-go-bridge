package bot

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/httpserver"
)

// BuildMagicLinkCardMap builds the interactive Feishu card for dashboard 2-hour magic login.
func BuildMagicLinkCardMap(targetURL string, expiresAt time.Time, openID string, ttl time.Duration) map[string]any {
	ttlDesc := "2 小时"
	if ttl > 0 {
		ttlDesc = fmt.Sprintf("%v", ttl)
	}

	contentLines := []string{
		"👋 **已为您生成管理控制台临时免密访问凭证。**",
		"",
		fmt.Sprintf("👤 **授权身份**：`%s`", openID),
		fmt.Sprintf("⏱️ **有效时长**：%s", ttlDesc),
		fmt.Sprintf("⏰ **失效时间**：%s", expiresAt.Format("2006-01-02 15:04:05")),
		"🛡️ **访问范围**：📊 监控概览 · 📜 单据审计流水 · 💻 实时系统运行日志",
	}

	elements := []any{
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": strings.Join(contentLines, "\n"),
			},
		},
		map[string]any{
			"tag": "hr",
		},
		map[string]any{
			"tag": "action",
			"actions": []any{
				map[string]any{
					"tag": "button",
					"text": map[string]any{
						"tag":     "plain_text",
						"content": "进入管理控制台 ➔",
					},
					"type": "primary",
					"url":  targetURL,
				},
			},
		},
		map[string]any{
			"tag": "note",
			"elements": []any{
				map[string]any{
					"tag":     "plain_text",
					"content": "⚠️ 安全提示：此链接包含临时凭证，请勿转发他人；2 小时后自动失效，失效后可随时再次私信发送【管理页面】获取新链接。",
				},
			},
		},
	}

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【TWC 报销管理】控制台登录授权",
			},
		},
		"elements": elements,
	}
}

// BuildMagicLinkCardJSON serializes the magic link card to JSON.
func BuildMagicLinkCardJSON(targetURL string, expiresAt time.Time, openID string, ttl time.Duration) (string, error) {
	b, err := json.Marshal(BuildMagicLinkCardMap(targetURL, expiresAt, openID, ttl))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildUnauthorizedCardMap builds the card indicating the user is not in admin whitelist.
func BuildUnauthorizedCardMap(openID string) map[string]any {
	contentLines := []string{
		"⚠️ **访问未授权**",
		"",
		fmt.Sprintf("您当前的飞书账号（OpenID: `%s`）尚未开通管理看板访问权限。", openID),
		"",
		"如需开通，请联系系统管理员在 `configs/config.toml` 中配置您的 OpenID 白名单。",
	}

	elements := []any{
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": strings.Join(contentLines, "\n"),
			},
		},
		map[string]any{
			"tag": "note",
			"elements": []any{
				map[string]any{
					"tag":     "plain_text",
					"content": "安全策略：管理控制台包含企业发票台账与单据机审明细，仅对配置的管理员开放。",
				},
			},
		},
	}

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "orange",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【TWC 报销管理】访问权限提示",
			},
		},
		"elements": elements,
	}
}

// BuildUnauthorizedCardJSON serializes the unauthorized card to JSON.
func BuildUnauthorizedCardJSON(openID string) (string, error) {
	b, err := json.Marshal(BuildUnauthorizedCardMap(openID))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildStatusCardMap builds the live service health card.
func BuildStatusCardMap(st httpserver.Status, baseURL string) map[string]any {
	feishuStatus := "🟢 " + st.FeishuState
	if st.FeishuState != "ready" {
		feishuStatus = "🟡 " + st.FeishuState
	}

	receiptProv := st.ReceiptProvider
	if receiptProv == "" {
		receiptProv = "未配置"
	}
	reviewProv := st.ReviewProvider
	if reviewProv == "" {
		reviewProv = "未配置"
	}

	contentLines := []string{
		"🟢 **系统健康度**：100% HEALTHY",
		"",
		fmt.Sprintf("📦 **服务版本**：`%s`", st.Version),
		fmt.Sprintf("⏱️ **运行时长**：%s", st.Uptime),
		fmt.Sprintf("📡 **飞书长连接**：%s（已处理 %d 个事件）", feishuStatus, st.FeishuEvents),
		fmt.Sprintf("🤖 **票据识别引擎**：%s", receiptProv),
		fmt.Sprintf("⚖️ **单据AI审批引擎**：%s", reviewProv),
	}

	elements := []any{
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": strings.Join(contentLines, "\n"),
			},
		},
		map[string]any{
			"tag": "hr",
		},
		map[string]any{
			"tag": "note",
			"elements": []any{
				map[string]any{
					"tag":     "plain_text",
					"content": "提示：私信发送【管理页面】可获取 2 小时免密访问控制台的专属链接。",
				},
			},
		},
	}

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "turquoise",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【TWC 报销管理】系统实时运行状态",
			},
		},
		"elements": elements,
	}
}

// BuildStatusCardJSON serializes the status card to JSON.
func BuildStatusCardJSON(st httpserver.Status, baseURL string) (string, error) {
	b, err := json.Marshal(BuildStatusCardMap(st, baseURL))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildHelpCardMap builds the command instructions card.
func BuildHelpCardMap() map[string]any {
	contentLines := []string{
		"💡 **我是 TWC 报销助手机器人，支持以下私信指令：**",
		"",
		"🔹 **发送「管理页面」或「看板」**",
		"立即生成 **2 小时免密专属登录链接**，一键进入监控看板（监控概览、单据审计与实时运行日志）。",
		"",
		"🔹 **发送「状态」或「status」**",
		"即时查询服务健康状态、长连接稳定性、运行时长与引擎工作模式。",
		"",
		"🔹 **发送「帮助」或「help」**",
		"获取本指令使用指引。",
	}

	elements := []any{
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": strings.Join(contentLines, "\n"),
			},
		},
		map[string]any{
			"tag": "hr",
		},
		map[string]any{
			"tag": "note",
			"elements": []any{
				map[string]any{
					"tag":     "plain_text",
					"content": "提示：私信单聊直接发送即可；在群聊中使用时需 @影视飓风 机器人。",
				},
			},
		},
	}

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【TWC 财务助手】对话指令菜单",
			},
		},
		"elements": elements,
	}
}

// BuildHelpCardJSON serializes the help card to JSON.
func BuildHelpCardJSON() (string, error) {
	b, err := json.Marshal(BuildHelpCardMap())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildUnknownCommandCardMap builds the prompt card for unrecognized inputs.
func BuildUnknownCommandCardMap(input string) map[string]any {
	contentLines := []string{
		fmt.Sprintf("收到您的消息：「%s」", input),
		"",
		"TWC 财务助手暂未识别该指令，您可以尝试：",
		"👉 发送 **【管理页面】**：获取 2 小时后台免密访问链接",
		"👉 发送 **【状态】**：查看系统运行指标与健康度",
		"👉 发送 **【帮助】**：查看完整指令列表",
	}

	elements := []any{
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": strings.Join(contentLines, "\n"),
			},
		},
	}

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "grey",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【TWC 财务助手】指令提示",
			},
		},
		"elements": elements,
	}
}

// BuildUnknownCommandCardJSON serializes the unknown command card to JSON.
func BuildUnknownCommandCardJSON(input string) (string, error) {
	b, err := json.Marshal(BuildUnknownCommandCardMap(input))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
