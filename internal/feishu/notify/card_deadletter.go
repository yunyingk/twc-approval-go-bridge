package notify

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// BuildDeadLetterCardMap constructs the Feishu interactive card payload for a dead-letter event.
func BuildDeadLetterCardMap(notice DeadLetterNotice) map[string]any {
	var recordDisplay string
	if notice.RecordURL != "" {
		recordDisplay = fmt.Sprintf("[%s](%s)", notice.RecordID, notice.RecordURL)
	} else {
		recordDisplay = notice.RecordID
	}

	failedAtStr := notice.FailedAt.Format("2006-01-02 15:04:05")
	if notice.FailedAt.IsZero() {
		failedAtStr = time.Now().Format("2006-01-02 15:04:05")
	}

	contentLines := []string{
		"⚠️ **小票识别连续失败已达上限，已自动进入死信保护并暂停重试，防止产生无效费用。**",
		"",
		fmt.Sprintf("**关联单据**：%s", recordDisplay),
		fmt.Sprintf("**小票文件**：%s", notice.FileName),
		fmt.Sprintf("**附件标识**：`%s`", notice.FileToken),
		fmt.Sprintf("**重试次数**：%d / 3 次（已终止）", notice.Attempts),
		fmt.Sprintf("**触发时间**：%s", failedAtStr),
		"",
		fmt.Sprintf("**最后错误**：\n```text\n%s\n```", strings.TrimSpace(notice.LastError)),
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
	}

	if notice.RecordURL != "" {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{
				map[string]any{
					"tag": "button",
					"text": map[string]any{
						"tag":     "plain_text",
						"content": "在飞书中查看单据",
					},
					"type": "danger",
					"url":  notice.RecordURL,
				},
			},
		})
	}

	elements = append(elements, map[string]any{
		"tag": "note",
		"elements": []any{
			map[string]any{
				"tag":     "plain_text",
				"content": "终端排查建议: 运行 ./bin/twc-approval-go-bridge list-failed-receipts 查看死信任务",
			},
		},
	})

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "carmine",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【海外小票识别】单据异常挂起（死信保护）",
			},
		},
		"elements": elements,
	}
}

// BuildDeadLetterCardJSON returns the serialized JSON payload of the dead-letter card.
func BuildDeadLetterCardJSON(notice DeadLetterNotice) (string, error) {
	cardMap := BuildDeadLetterCardMap(notice)
	bytes, err := json.Marshal(cardMap)
	if err != nil {
		return "", fmt.Errorf("marshal dead letter card: %w", err)
	}
	return string(bytes), nil
}

// BuildTestCardMap constructs an interactive card for connectivity diagnostics.
func BuildTestCardMap(notice TestNotice) map[string]any {
	tsStr := notice.Timestamp.Format("2006-01-02 15:04:05")
	if notice.Timestamp.IsZero() {
		tsStr = time.Now().Format("2006-01-02 15:04:05")
	}

	note := notice.Note
	if note == "" {
		note = "这是一条来自海外小票识别与 AI 审核桥接服务的通道连通性测试消息。"
	}

	contentLines := []string{
		"🎉 **飞书机器人告警通知通道配置成功且正常连通！**",
		"",
		fmt.Sprintf("**测试触发时间**：%s", tsStr),
	}
	if notice.TriggeredBy != "" {
		contentLines = append(contentLines, fmt.Sprintf("**触发主体**：%s", notice.TriggeredBy))
	}
	contentLines = append(contentLines, "", fmt.Sprintf("**说明**：%s", note))

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "【系统通知】飞书告警通道连通性测试",
			},
		},
		"elements": []any{
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
						"content": "来自 海外小票识别桥接服务 (ebot)",
					},
				},
			},
		},
	}
}

// BuildTestCardJSON returns the serialized JSON payload of the test notice card.
func BuildTestCardJSON(notice TestNotice) (string, error) {
	cardMap := BuildTestCardMap(notice)
	bytes, err := json.Marshal(cardMap)
	if err != nil {
		return "", fmt.Errorf("marshal test card: %w", err)
	}
	return string(bytes), nil
}
