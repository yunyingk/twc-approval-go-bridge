package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/audit"
)

// Config defines the notification routing configuration.
type Config struct {
	Enabled            bool
	ChatIDs            []string
	OpenIDs            []string
	NotifyOnDeadLetter bool
}

// Service dispatches operational alert cards to Feishu chats and individual users.
type Service struct {
	cfg      Config
	client   *Client
	logger   *slog.Logger
	mu       sync.Mutex
	notified map[string]time.Time // key: RecordID:FileToken -> last notice time
}

// NewService instantiates a notification service.
func NewService(cfg Config, client *Client, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:      cfg,
		client:   client,
		logger:   logger,
		notified: make(map[string]time.Time),
	}
}

// NotifyDeadLetter broadcasts a failure alert card to all configured chat groups and users.
func (s *Service) NotifyDeadLetter(ctx context.Context, notice DeadLetterNotice) error {
	if !s.cfg.Enabled || !s.cfg.NotifyOnDeadLetter {
		return nil
	}
	if len(s.cfg.ChatIDs) == 0 && len(s.cfg.OpenIDs) == 0 {
		s.logger.DebugContext(ctx, "dead letter notification enabled but no chat_ids or open_ids configured",
			"record_id", notice.RecordID, "file_token", notice.FileToken)
		return nil
	}

	dedupKey := notice.RecordID + ":" + notice.FileToken
	s.mu.Lock()
	if last, ok := s.notified[dedupKey]; ok && time.Since(last) < 1*time.Hour {
		s.mu.Unlock()
		s.logger.InfoContext(ctx, "skip duplicate dead letter notice",
			"record_id", notice.RecordID, "file_token", notice.FileToken, "last_notified", last)
		return nil
	}
	s.notified[dedupKey] = time.Now()
	s.mu.Unlock()

	cardJSON, err := BuildDeadLetterCardJSON(notice)
	if err != nil {
		return fmt.Errorf("build dead letter card: %w", err)
	}

	results, err := s.broadcastCard(ctx, cardJSON, "dead_letter", notice.RecordID)

	// Record in audit log
	successCount := 0
	for _, res := range results {
		if res.MessageID != "" {
			successCount++
		}
	}
	audit.Log(audit.Entry{
		Timestamp: time.Now(),
		Type:      "dead_letter_alert",
		RecordID:  notice.RecordID,
		Action:    fmt.Sprintf("发送死信告警卡片 (成功 %d / 总计 %d)", successCount, len(results)),
		Details: map[string]any{
			"file_name":     notice.FileName,
			"file_token":    notice.FileToken,
			"attempts":      notice.Attempts,
			"error":         notice.LastError,
			"targets_count": len(results),
			"success_count": successCount,
		},
	})

	return err
}

// SendTestCard dispatches a test connectivity card to all configured chat groups and users.
func (s *Service) SendTestCard(ctx context.Context, notice TestNotice) ([]DeliveryResult, error) {
	if len(s.cfg.ChatIDs) == 0 && len(s.cfg.OpenIDs) == 0 {
		return nil, errors.New("no chat_ids or open_ids configured in notification settings")
	}

	cardJSON, err := BuildTestCardJSON(notice)
	if err != nil {
		return nil, fmt.Errorf("build test card: %w", err)
	}

	return s.broadcastCard(ctx, cardJSON, "test_connectivity", "")
}

func (s *Service) broadcastCard(ctx context.Context, cardJSON, eventType, recordID string) ([]DeliveryResult, error) {
	var results []DeliveryResult
	var errs []string

	for _, chatID := range s.cfg.ChatIDs {
		res := DeliveryResult{
			TargetType: "chat_id",
			TargetID:   chatID,
		}
		msgID, err := s.client.SendCard(ctx, "chat_id", chatID, cardJSON)
		if err != nil {
			res.Error = err.Error()
			errs = append(errs, fmt.Sprintf("chat_id %s: %v", chatID, err))
			s.logger.ErrorContext(ctx, "failed to dispatch notification card to chat",
				"event", eventType, "record_id", recordID, "chat_id", chatID, "error", err)
		} else {
			res.MessageID = msgID
			s.logger.InfoContext(ctx, "dispatched notification card to chat",
				"event", eventType, "record_id", recordID, "chat_id", chatID, "message_id", msgID)
		}
		results = append(results, res)
	}

	for _, openID := range s.cfg.OpenIDs {
		res := DeliveryResult{
			TargetType: "open_id",
			TargetID:   openID,
		}
		msgID, err := s.client.SendCard(ctx, "open_id", openID, cardJSON)
		if err != nil {
			res.Error = err.Error()
			errs = append(errs, fmt.Sprintf("open_id %s: %v", openID, err))
			s.logger.ErrorContext(ctx, "failed to dispatch notification card to user",
				"event", eventType, "record_id", recordID, "open_id", openID, "error", err)
		} else {
			res.MessageID = msgID
			s.logger.InfoContext(ctx, "dispatched notification card to user",
				"event", eventType, "record_id", recordID, "open_id", openID, "message_id", msgID)
		}
		results = append(results, res)
	}

	if len(errs) > 0 {
		return results, fmt.Errorf("broadcast notification had %d failure(s): %s", len(errs), errors.Join(errorsSlice(errs)...).Error())
	}

	return results, nil
}

func errorsSlice(errStrs []string) []error {
	out := make([]error, len(errStrs))
	for i, s := range errStrs {
		out[i] = errors.New(s)
	}
	return out
}
