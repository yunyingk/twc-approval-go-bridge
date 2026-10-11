package bot

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Intent represents the parsed user intention from a Feishu message.
type Intent string

const (
	IntentDashboard Intent = "dashboard"
	IntentStatus    Intent = "status"
	IntentHelp      Intent = "help"
	IntentUnknown   Intent = "unknown"
)

// MessageReceiveEvent represents the envelope of the Feishu im.message.receive_v1 event.
type MessageReceiveEvent struct {
	Schema string `json:"schema"`
	Header struct {
		EventID    string `json:"event_id"`
		EventType  string `json:"event_type"`
		CreateTime string `json:"create_time"`
		Token      string `json:"token"`
		AppID      string `json:"app_id"`
		TenantKey  string `json:"tenant_key"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				UnionID string `json:"union_id"`
				UserID  string `json:"user_id"`
				OpenID  string `json:"open_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"` // "user", "app"
			TenantKey  string `json:"tenant_key"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			RootID      string `json:"root_id"`
			ParentID    string `json:"parent_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"` // "p2p", "group"
			MessageType string `json:"message_type"` // "text", ...
			Content     string `json:"content"` // JSON-encoded string
			Mentions    []struct {
				Key      string `json:"key"`
				ID       struct {
					OpenID string `json:"open_id"`
				} `json:"id"`
				Name string `json:"name"`
			} `json:"mentions"`
		} `json:"message"`
	} `json:"event"`
}

var mentionRegex = regexp.MustCompile(`@_user_\d+|@\S+`)

// ExtractPlainText extracts and normalizes user text from Feishu message content.
// It removes Feishu @mention placeholders like @_user_1 or @影视飓风 and trims whitespace.
func ExtractPlainText(msgType, content string) string {
	if msgType != "text" {
		return ""
	}

	var data struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return strings.TrimSpace(content)
	}

	clean := mentionRegex.ReplaceAllString(data.Text, " ")
	return strings.TrimSpace(clean)
}

// MatchIntent classifies the user's input into a recognized command intent.
func MatchIntent(text string) Intent {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return IntentUnknown
	}

	switch {
	case containsAny(normalized, "管理页面", "管理看板", "控制台", "管理后台", "后台", "看板", "dashboard", "console"):
		return IntentDashboard
	case containsAny(normalized, "状态", "status", "健康", "health", "运行状态", "系统状态"):
		return IntentStatus
	case containsAny(normalized, "帮助", "help", "菜单", "功能", "指引", "?", "？"):
		return IntentHelp
	default:
		return IntentUnknown
	}
}

func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
