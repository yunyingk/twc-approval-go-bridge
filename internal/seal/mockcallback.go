package seal

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
)

// MockCallback is a local-only protocol fixture. It acknowledges a simulated
// Seal decision but does not approve a Feishu record or persist a result.
func MockCallback(logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "local callback mock only", http.StatusForbidden)
			return
		}
		var payload struct {
			DocumentID       string `json:"documentId"`
			ApprovalRecordID string `json:"approvalRecordId"`
			Decision         string `json:"decision"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&payload); err != nil ||
			payload.DocumentID == "" || payload.ApprovalRecordID == "" ||
			(payload.Decision != "approve" && payload.Decision != "reject" && payload.Decision != "review") {
			http.Error(w, "invalid mock callback", http.StatusBadRequest)
			return
		}
		logger.InfoContext(r.Context(), "mock Seal callback received", "document_id", payload.DocumentID,
			"approval_record_id", payload.ApprovalRecordID, "decision", payload.Decision)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	})
}
