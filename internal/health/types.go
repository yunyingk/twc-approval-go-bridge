// Package health provides diagnostic health checks and configuration probing.
package health

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Status represents the health check result for a single item.
type Status string

const (
	StatusPass Status = "PASS"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

// CheckItem represents a single probed health check item.
type CheckItem struct {
	Category string        `json:"category"`
	Name     string        `json:"name"`
	Status   Status        `json:"status"`
	Message  string        `json:"message"`
	Latency  time.Duration `json:"latency_ms"`
	Remedy   string        `json:"remedy,omitempty"`
}

// Report aggregates all health check items and overall readiness.
type Report struct {
	Total         int         `json:"total"`
	Passed        int         `json:"passed"`
	Warned        int         `json:"warned"`
	Failed        int         `json:"failed"`
	OverallStatus string      `json:"overall_status"`
	Duration      string      `json:"duration"`
	Items         []CheckItem `json:"items"`
}

// RenderJSON serializes the report to structured JSON.
func (r Report) RenderJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// RenderText prints a formatted, human-readable terminal report.
func (r Report) RenderText(w io.Writer) {
	fmt.Fprintln(w, "======================================================================")
	fmt.Fprintln(w, "🏥 海外易商卡报销网桥 · 环境健康探测 (Health Doctor)")
	fmt.Fprintln(w, "======================================================================")

	currentCategory := ""
	catIndex := 0

	for _, item := range r.Items {
		if item.Category != currentCategory {
			currentCategory = item.Category
			catIndex++
			fmt.Fprintf(w, "\n[%d] %s\n", catIndex, currentCategory)
		}

		icon := "✅"
		statusColor := "\033[32m" // Green
		switch item.Status {
		case StatusWarn:
			icon = "⚠️ "
			statusColor = "\033[33m" // Yellow
		case StatusFail:
			icon = "❌"
			statusColor = "\033[31m" // Red
		}
		resetColor := "\033[0m"

		latencyStr := ""
		if item.Latency > 0 {
			latencyStr = fmt.Sprintf(" (%dms)", item.Latency.Milliseconds())
		}

		fmt.Fprintf(w, "    %s %s: %s%s%s%s\n", icon, item.Name, statusColor, item.Message, resetColor, latencyStr)
		if item.Remedy != "" {
			lines := strings.Split(item.Remedy, "\n")
			for _, line := range lines {
				fmt.Fprintf(w, "       ↳ %s\n", line)
			}
		}
	}

	fmt.Fprintln(w, "\n======================================================================")
	summaryIcon := "🎉"
	summaryColor := "\033[32m"
	if r.Failed > 0 {
		summaryIcon = "🚨"
		summaryColor = "\033[31m"
	} else if r.Warned > 0 {
		summaryIcon = "⚠️ "
		summaryColor = "\033[33m"
	}

	fmt.Fprintf(w, "%s 最终评估: %s%s%s (共 %d 项: %d 通过, %d 警告, %d 失败 · 耗时 %s)\n",
		summaryIcon,
		summaryColor, r.OverallStatus, "\033[0m",
		r.Total, r.Passed, r.Warned, r.Failed, r.Duration,
	)
	fmt.Fprintln(w, "======================================================================")
}
