package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/notify"
	"github.com/yunyingk/twc-approval-go-bridge/internal/httpserver"
)

// Config configures the Feishu Bot auto-responder.
type Config struct {
	PublicBaseURL string
	AdminOpenIDs  []string
	TokenTTL      time.Duration
}

type deduplicator struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newDeduplicator() *deduplicator {
	return &deduplicator{seen: make(map[string]time.Time)}
}

func (d *deduplicator) seenOrAdd(id string, ttl time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for k, exp := range d.seen {
		if now.After(exp) {
			delete(d.seen, k)
		}
	}

	if _, exists := d.seen[id]; exists {
		return true
	}
	d.seen[id] = now.Add(ttl)
	return false
}

// Handler orchestrates interactive message reception and responsive answers.
type Handler struct {
	config         Config
	client         *notify.Client
	tokenManager   *httpserver.TokenManager
	statusProvider httpserver.StatusProvider
	adminSet       map[string]bool
	logger         *slog.Logger
	dedup          *deduplicator
}

// NewHandler constructs a ready-to-serve Feishu Bot message handler.
func NewHandler(
	cfg Config,
	client *notify.Client,
	tm *httpserver.TokenManager,
	sp httpserver.StatusProvider,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")

	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = 2 * time.Hour
	}

	admins := make(map[string]bool, len(cfg.AdminOpenIDs))
	for _, id := range cfg.AdminOpenIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed != "" {
			admins[trimmed] = true
		}
	}

	return &Handler{
		config:         cfg,
		client:         client,
		tokenManager:   tm,
		statusProvider: sp,
		adminSet:       admins,
		logger:         logger,
		dedup:          newDeduplicator(),
	}
}

// Sink receives transport-level events and responds to recognized Feishu messages.
func (h *Handler) Sink(ctx context.Context, ev events.Event) error {
	if ev.Type != "im.message.receive_v1" {
		return nil
	}

	var envelope MessageReceiveEvent
	if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
		h.logger.WarnContext(ctx, "failed to decode Feishu im message event", "error", err)
		return nil
	}

	sender := envelope.Event.Sender
	msg := envelope.Event.Message

	// Ignore non-user senders to eliminate bot-to-bot infinite reply loops
	if sender.SenderType != "" && sender.SenderType != "user" {
		return nil
	}

	msgID := strings.TrimSpace(msg.MessageID)
	if msgID == "" {
		return nil
	}

	// De-duplicate retried delivery events
	if h.dedup.seenOrAdd(msgID, 5*time.Minute) {
		h.logger.DebugContext(ctx, "skipping duplicated feishu message", "message_id", msgID)
		return nil
	}

	text := ExtractPlainText(msg.MessageType, msg.Content)
	if text == "" {
		return nil
	}

	senderOpenID := strings.TrimSpace(sender.SenderID.OpenID)
	chatType := strings.TrimSpace(msg.ChatType)
	chatID := strings.TrimSpace(msg.ChatID)

	intent := MatchIntent(text)
	h.logger.InfoContext(ctx, "feishu interactive message received",
		"intent", string(intent),
		"sender_openid", senderOpenID,
		"chat_type", chatType,
		"text", text,
	)

	return h.handleIntent(ctx, intent, text, msgID, chatID, chatType, senderOpenID)
}

func (h *Handler) handleIntent(
	ctx context.Context,
	intent Intent,
	rawText, msgID, chatID, chatType, senderOpenID string,
) error {
	switch intent {
	case IntentDashboard:
		return h.handleDashboardCommand(ctx, msgID, chatID, chatType, senderOpenID)
	case IntentStatus:
		return h.handleStatusCommand(ctx, msgID, chatID, chatType, senderOpenID)
	case IntentHelp:
		return h.handleHelpCommand(ctx, msgID, chatID, chatType, senderOpenID)
	default:
		// In 1-on-1 private chat (p2p), guide the user on available commands.
		// In group chat, silence unrecognized chatter to avoid spamming the group.
		if chatType == "p2p" {
			return h.handleUnknownCommand(ctx, rawText, msgID, chatID, senderOpenID)
		}
		return nil
	}
}

func (h *Handler) handleDashboardCommand(
	ctx context.Context,
	msgID, chatID, chatType, senderOpenID string,
) error {
	if !h.isAuthorized(senderOpenID) {
		h.logger.WarnContext(ctx, "dashboard access rejected: unauthorized user", "open_id", senderOpenID)
		cardJSON, err := BuildUnauthorizedCardJSON(senderOpenID)
		if err != nil {
			return err
		}
		return h.replyOrSend(ctx, msgID, chatID, chatType, senderOpenID, cardJSON)
	}

	if strings.TrimSpace(h.config.PublicBaseURL) == "" {
		h.logger.WarnContext(ctx, "dashboard requested but public_base_url is not configured", "open_id", senderOpenID)
		text := "系统未配置公网访问地址（runtime.public_base_url），请先在配置文件中指定后再使用管理看板功能。"
		cardJSON := `{"elements":[{"tag":"div","text":{"tag":"lark_md","content":"⚠️ ` + text + `"}}],"header":{"title":{"tag":"plain_text","content":"看板地址未配置"},"template":"orange"}}`
		return h.replyOrSend(ctx, msgID, chatID, chatType, senderOpenID, cardJSON)
	}

	// Generate 2-hour magic token
	token := h.tokenManager.Generate(senderOpenID, "管理员", h.config.TokenTTL)
	magicURL := fmt.Sprintf("%s/?token=%s", h.config.PublicBaseURL, token.Token)

	cardJSON, err := BuildMagicLinkCardJSON(magicURL, token.ExpiresAt, senderOpenID, h.config.TokenTTL)
	if err != nil {
		return err
	}

	// In group chats, deliver the token card privately to the user to avoid exposing it to the room
	if chatType == "group" {
		if _, err := h.client.SendCard(ctx, "open_id", senderOpenID, cardJSON); err != nil {
			h.logger.WarnContext(ctx, "failed to send private magic card to group requester", "error", err)
		}
		noticeCard, err := buildGroupPrivacyNoticeJSON(senderOpenID)
		if err == nil {
			_, _ = h.client.ReplyCard(ctx, msgID, noticeCard)
		}
		return nil
	}

	return h.replyOrSend(ctx, msgID, chatID, chatType, senderOpenID, cardJSON)
}

func (h *Handler) handleStatusCommand(
	ctx context.Context,
	msgID, chatID, chatType, senderOpenID string,
) error {
	var st httpserver.Status
	if h.statusProvider != nil {
		st = h.statusProvider()
	}

	cardJSON, err := BuildStatusCardJSON(st, h.config.PublicBaseURL)
	if err != nil {
		return err
	}
	return h.replyOrSend(ctx, msgID, chatID, chatType, senderOpenID, cardJSON)
}

func (h *Handler) handleHelpCommand(
	ctx context.Context,
	msgID, chatID, chatType, senderOpenID string,
) error {
	cardJSON, err := BuildHelpCardJSON()
	if err != nil {
		return err
	}
	return h.replyOrSend(ctx, msgID, chatID, chatType, senderOpenID, cardJSON)
}

func (h *Handler) handleUnknownCommand(
	ctx context.Context,
	rawText, msgID, chatID, senderOpenID string,
) error {
	cardJSON, err := BuildUnknownCommandCardJSON(rawText)
	if err != nil {
		return err
	}
	return h.replyOrSend(ctx, msgID, chatID, "p2p", senderOpenID, cardJSON)
}

func (h *Handler) replyOrSend(
	ctx context.Context,
	msgID, chatID, chatType, senderOpenID, cardJSON string,
) error {
	if h.client == nil {
		return nil
	}

	// Try in-thread reply first
	if msgID != "" {
		if _, err := h.client.ReplyCard(ctx, msgID, cardJSON); err == nil {
			return nil
		} else {
			h.logger.DebugContext(ctx, "reply message failed, falling back to direct delivery", "error", err)
		}
	}

	// Fallback to sending to open_id or chat_id
	if chatType == "p2p" && senderOpenID != "" {
		_, err := h.client.SendCard(ctx, "open_id", senderOpenID, cardJSON)
		return err
	}
	if chatID != "" {
		_, err := h.client.SendCard(ctx, "chat_id", chatID, cardJSON)
		return err
	}
	return nil
}

func (h *Handler) isAuthorized(openID string) bool {
	if len(h.adminSet) == 0 {
		return true // If no explicit admin whitelist is configured, allow
	}
	return h.adminSet[openID]
}

func buildGroupPrivacyNoticeJSON(openID string) (string, error) {
	cardMap := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【TWC 报销管理】授权凭证已发放",
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": fmt.Sprintf("🔒 **安全提示**：为防止凭证外泄，管理看板 2 小时临时免密授权链接已通过机器人**私信发送给您**（<at id=\"%s\"></at>），请在单聊窗口中查收。", openID),
				},
			},
		},
	}
	b, err := json.Marshal(cardMap)
	return string(b), err
}
