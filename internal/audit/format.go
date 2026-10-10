package audit

import (
	"fmt"
	"strings"
)

// IconForType maps event types to intuitive terminal icons.
func IconForType(t EventType) string {
	switch t {
	case TypeNotifyCardSent:
		return "📱"
	case TypeNotifyCardSkipped:
		return "⏭️"
	case TypeFileUploaded:
		return "📎"
	case TypeOCRInitiated:
		return "🔍"
	case TypeOCRCompleted:
		return "✅"
	case TypeOCRFailed:
		return "❌"
	case TypeReviewSubmitted:
		return "⚖️"
	case TypeReviewCompleted:
		return "🎯"
	case TypeReviewWriteback:
		return "📝"
	default:
		return "🔹"
	}
}

// LabelForType returns a readable Chinese category tag.
func LabelForType(t EventType) string {
	switch t {
	case TypeNotifyCardSent:
		return "推送卡片"
	case TypeNotifyCardSkipped:
		return "跳过推送"
	case TypeFileUploaded:
		return "文件上传"
	case TypeOCRInitiated:
		return "发起OCR"
	case TypeOCRCompleted:
		return "OCR完成"
	case TypeOCRFailed:
		return "OCR失败"
	case TypeReviewSubmitted:
		return "发起审核"
	case TypeReviewCompleted:
		return "机审完成"
	case TypeReviewWriteback:
		return "结果回写"
	default:
		return string(t)
	}
}

// FormatEntry formats a single audit log entry for terminal display.
func FormatEntry(e Entry) string {
	timeStr := e.Timestamp.Format("2006-01-02 15:04:05")
	icon := IconForType(e.Type)
	label := LabelForType(e.Type)

	var actor string
	if e.OperatorName != "" && e.Operator != "" {
		actor = fmt.Sprintf("[%s / %s]", e.OperatorName, e.Operator)
	} else if e.OperatorName != "" {
		actor = fmt.Sprintf("[%s]", e.OperatorName)
	} else if e.Operator != "" {
		actor = fmt.Sprintf("[%s]", e.Operator)
	}

	details := formatDetails(e.Details)
	if details != "" {
		details = " " + details
	}

	if actor != "" {
		return fmt.Sprintf("[%s] %s 【%s】 %s %s%s", timeStr, icon, label, actor, e.Action, details)
	}
	return fmt.Sprintf("[%s] %s 【%s】 %s%s", timeStr, icon, label, e.Action, details)
}

// FormatTimeline renders an ASCII tree timeline for a single record's lifecycle.
func FormatTimeline(recordID string, entries []Entry) string {
	if len(entries) == 0 {
		return fmt.Sprintf("单据 [%s] 暂无业务生命周期日志记录", recordID)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("================================================================================\n"))
	b.WriteString(fmt.Sprintf("📜 单据 [%s] 业务生命周期演进时间线 (共 %d 个阶段事件):\n", recordID, len(entries)))
	b.WriteString(fmt.Sprintf("================================================================================\n"))

	for i, e := range entries {
		prefix := "├─"
		if i == len(entries)-1 {
			prefix = "└─"
		}
		timeStr := e.Timestamp.Format("2006-01-02 15:04:05")
		icon := IconForType(e.Type)
		label := LabelForType(e.Type)

		var actor string
		if e.OperatorName != "" && e.Operator != "" {
			actor = fmt.Sprintf("%s (%s)", e.OperatorName, e.Operator)
		} else if e.OperatorName != "" {
			actor = e.OperatorName
		} else if e.Operator != "" {
			actor = e.Operator
		}

		line := fmt.Sprintf("%s [%s] %s 【%s】", prefix, timeStr, icon, label)
		if actor != "" {
			line += fmt.Sprintf(" 人员: %s |", actor)
		}
		line += fmt.Sprintf(" %s", e.Action)

		b.WriteString(line + "\n")
		details := formatDetails(e.Details)
		if details != "" {
			indent := "│  "
			if i == len(entries)-1 {
				indent = "   "
			}
			b.WriteString(fmt.Sprintf("%s   元数据: %s\n", indent, details))
		}
	}
	b.WriteString(fmt.Sprintf("================================================================================\n"))
	return b.String()
}

func formatDetails(d map[string]any) string {
	if len(d) == 0 {
		return ""
	}
	var parts []string
	for k, v := range d {
		if v == nil || v == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
