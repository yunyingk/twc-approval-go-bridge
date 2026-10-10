package card

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// CandidateRow contains preview information for an un-notified, valid transaction.
type CandidateRow struct {
	RecordID      string `json:"record_id"`
	TransactionID string `json:"transaction_id"`
	Merchant      string `json:"merchant"`
	BookedAmount  string `json:"booked_amount"`
	Cardholder    string `json:"cardholder"`
	OpenID        string `json:"open_id"`
}

// ScanReport summarizes the outcome of a batch scan pass.
type ScanReport struct {
	TotalRows         int            `json:"total_rows"`
	SkippedLinked     int            `json:"skipped_linked"`
	SkippedZeroAmount int            `json:"skipped_zero_amount"`
	SkippedStatus     int            `json:"skipped_status"`
	SkippedClaim      int            `json:"skipped_claim"`
	SkippedNoUser     int            `json:"skipped_no_user"`
	Candidates        []CandidateRow `json:"candidates"`
	NotifiedCount     int            `json:"notified_count"`
	DryRun            bool           `json:"dry_run"`
	Duration          time.Duration  `json:"duration"`
}

// ScanOptions controls scanner execution.
type ScanOptions struct {
	Send  bool   // If false (default), only inspect and report candidates without sending cards or creating tasks.
	Limit int    // Maximum number of notifications to send (0 = all eligible candidates).
	User  string // Optional filter by recipient name or open_id.
}

// Scanner performs fast, paginated inspection across transactions table
// using dedicated in-memory business rules from filter.go.
type Scanner struct {
	svc    *Service
	logger *slog.Logger
}

// NewScanner instantiates a transaction scanner.
func NewScanner(svc *Service, logger *slog.Logger) *Scanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{
		svc:    svc,
		logger: logger,
	}
}

// Inspect evaluates all rows in memory in batches and returns a ScanReport.
func (s *Scanner) Inspect(ctx context.Context, opts ...ScanOptions) (*ScanReport, error) {
	start := time.Now()
	opt := ScanOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}

	transBinding := s.svc.cfg.Business.Tables.Transactions
	token, err := s.svc.client.accessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch access token: %w", err)
	}

	fieldMap, err := s.svc.fetchFieldMap(ctx, token, transBinding.BaseToken, transBinding.TableID)
	if err != nil {
		return nil, fmt.Errorf("fetch field map: %w", err)
	}

	rows, err := s.svc.ListTransactionRows(ctx)
	if err != nil {
		return nil, fmt.Errorf("list transaction rows: %w", err)
	}

	report := &ScanReport{
		TotalRows: len(rows),
		DryRun:    !opt.Send,
	}

	getFieldValue := func(record map[string]any, semantic string) any {
		if transBinding.Fields != nil {
			fieldID := transBinding.Fields[semantic]
			if fieldID != "" {
				fieldName := fieldMap[fieldID]
				if fieldName != "" {
					return record[fieldName]
				}
			}
		}
		return nil
	}

	var candidates []CandidateRow

	for _, row := range rows {
		decision := EvaluateTransactionFilter(row.Fields, fieldMap, transBinding)
		if !decision.ShouldNotify {
			switch {
			case strings.Contains(decision.Reason, "已关联"):
				report.SkippedLinked++
			case strings.Contains(decision.Reason, "0或负数"):
				report.SkippedZeroAmount++
			case strings.Contains(decision.Reason, "交易状态"):
				report.SkippedStatus++
			case strings.Contains(decision.Reason, "报销状态"):
				report.SkippedClaim++
			case strings.Contains(decision.Reason, "持卡人"):
				report.SkippedNoUser++
			}
			continue
		}

		cardholderRaw := getFieldValue(row.Fields, "cardholder")
		if cardholderRaw == nil {
			cardholderRaw = row.Fields["持卡人"]
		}
		openID, recipientName := extractCardholderOpenID(cardholderRaw)

		if opt.User != "" {
			if !strings.EqualFold(opt.User, openID) && !strings.Contains(recipientName, opt.User) {
				continue
			}
		}

		txID := formatString(getFieldValue(row.Fields, "transaction_id"), row.Fields["交易流水号"])
		merchant := formatString(getFieldValue(row.Fields, "merchant"), row.Fields["商户名称"])
		bookedAmt := formatString(getFieldValue(row.Fields, "booked_amount_cny"), row.Fields["结算金额"])
		bookedCur := formatString(row.Fields["结算金额币种"], "CNY")
		origAmt := formatString(getFieldValue(row.Fields, "original_amount"), row.Fields["交易金额"])
		origCur := formatString(getFieldValue(row.Fields, "original_currency"), row.Fields["交易币种"])

		displayAmt := bookedAmt
		displayCur := bookedCur
		if displayAmt == "" && origAmt != "" {
			displayAmt = origAmt
			displayCur = origCur
		}
		amountDisplay := strings.TrimSpace(fmt.Sprintf("%s %s", displayCur, displayAmt))
		if origAmt != "" && bookedAmt != "" && bookedAmt != origAmt {
			amountDisplay = fmt.Sprintf("%s %s (原币 %s %s)", bookedCur, bookedAmt, origCur, origAmt)
		}

		candidates = append(candidates, CandidateRow{
			RecordID:      row.RecordID,
			TransactionID: txID,
			Merchant:      merchant,
			BookedAmount:  amountDisplay,
			Cardholder:    recipientName,
			OpenID:        openID,
		})
	}

	report.Candidates = candidates

	// If send is enabled, notify candidates up to limit
	if opt.Send && len(candidates) > 0 {
		limit := len(candidates)
		if opt.Limit > 0 && opt.Limit < limit {
			limit = opt.Limit
		}

		for i := 0; i < limit; i++ {
			c := candidates[i]
			res, err := s.svc.NotifyTransaction(ctx, c.RecordID)
			if err != nil {
				s.logger.WarnContext(ctx, "failed to notify transaction", "record_id", c.RecordID, "error", err)
				continue
			}
			if res.Status == "success" || res.Status == "sent" {
				report.NotifiedCount++
				s.logger.InfoContext(ctx, "sent transaction reminder",
					"record_id", c.RecordID, "recipient", c.Cardholder, "tx_id", c.TransactionID)
			}
		}
	}

	report.Duration = time.Since(start)
	return report, nil
}

// Start begins periodic background inspection.
func (s *Scanner) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	s.logger.Info("starting transaction scanner", "interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial scan on startup (sends notifications for eligible candidates)
	if report, err := s.Inspect(ctx, ScanOptions{Send: true}); err != nil {
		s.logger.Warn("initial transaction scan failed", "error", err)
	} else {
		s.logger.Info("initial transaction scan complete",
			"total", report.TotalRows, "candidates", len(report.Candidates), "notified", report.NotifiedCount, "duration", report.Duration)
	}

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("transaction scanner stopped")
			return
		case <-ticker.C:
			if report, err := s.Inspect(ctx, ScanOptions{Send: true}); err != nil {
				s.logger.Warn("periodic transaction scan failed", "error", err)
			} else if report.NotifiedCount > 0 {
				s.logger.Info("transaction scan complete",
					"total", report.TotalRows, "candidates", len(report.Candidates), "notified", report.NotifiedCount, "duration", report.Duration)
			}
		}
	}
}
