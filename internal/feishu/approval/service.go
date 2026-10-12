package approval

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// DetailSource provides business records from Bitable.
type DetailSource interface {
	ReadDetail(ctx context.Context, recordID string) (core.Detail, error)
}

// ReviewReader optionally provides AI review attempts for a record.
type ReviewReader interface {
	ReadReview(ctx context.Context, documentID string) (core.Attempt, error)
}

// ServiceOptions configures the Approval Service.
type ServiceOptions struct {
	ApprovalCode     string
	DefaultApprover  string
	DefaultInitiator string
	Source           DetailSource
	ReviewReader     ReviewReader
}

// Service coordinates preparing and pushing approval instances to Feishu.
type Service struct {
	client  *Client
	options ServiceOptions
}

// NewService creates a new Feishu approval service.
func NewService(client *Client, opts ServiceOptions) *Service {
	if opts.ApprovalCode == "" {
		opts.ApprovalCode = DefaultApprovalCode
	}
	return &Service{
		client:  client,
		options: opts,
	}
}

// PrepareInput gathers facts for a reimbursement detail record into ApprovalInput.
func (s *Service) PrepareInput(ctx context.Context, recordID string) (ApprovalInput, error) {
	if s.options.Source == nil {
		return ApprovalInput{}, fmt.Errorf("detail source is not configured")
	}

	detail, err := s.options.Source.ReadDetail(ctx, recordID)
	if err != nil {
		return ApprovalInput{}, fmt.Errorf("read detail %s: %w", recordID, err)
	}

	input := ApprovalInput{
		RecordID:         recordID,
		DocumentSN:       detail.DocumentSN,
		SubmissionDate:   time.Now(),
		ApproverOpenID:   s.options.DefaultApprover,
		InitiatorOpenID:  s.options.DefaultInitiator,
		ParentRecordID:   recordID,
	}

	if reason := detail.Context["expense_reason"]; reason != "" {
		input.ExpenseReason = reason
	}
	if category := detail.Context["expense_category"]; category != "" {
		input.ExpenseCategory = category
	}

	if detail.Transactions != nil && len(detail.Transactions.Transactions) > 0 {
		tx := detail.Transactions.Transactions[0]
		input.TransactionID = tx.TransactionID
		input.ExpenseDate = tx.OccurredAt
		input.Currency = tx.OriginalCurrency
		if amt, err := strconv.ParseFloat(tx.OriginalAmount, 64); err == nil {
			input.OriginalAmount = amt
		}
		if amt, err := strconv.ParseFloat(tx.BookedAmountCNY, 64); err == nil {
			input.CNYAmount = amt
		}

		merchant := tx.Merchant
		if merchant == "" {
			merchant = "易商卡消费"
		}
		curr := tx.OriginalCurrency
		if curr == "" {
			curr = "CNY"
		}
		input.Title = fmt.Sprintf("海外易商卡消费确认 - %s %s %s", merchant, curr, tx.OriginalAmount)
	} else {
		input.Title = fmt.Sprintf("海外易商卡消费确认 - %s", recordID)
	}

	// Invoices count and keys
	if len(detail.Files) > 0 {
		keys := make([]string, 0, len(detail.Files))
		for _, f := range detail.Files {
			keys = append(keys, f.Token)
		}
		input.InvoiceKeys = strings.Join(keys, ", ")
	}

	// Try loading AI review outcome if ReviewReader is available
	if s.options.ReviewReader != nil && detail.DocumentID != "" {
		if attempt, err := s.options.ReviewReader.ReadReview(ctx, detail.DocumentID); err == nil && attempt.Submission != nil && attempt.Submission.Outcome != nil {
			outcome := attempt.Submission.Outcome
			switch strings.ToLower(outcome.Decision) {
			case "approve":
				input.ReviewStatus = "pass"
				input.RiskLevel = "low"
			case "reject":
				input.ReviewStatus = "reject"
				input.RiskLevel = "high"
			case "review":
				input.ReviewStatus = "review"
				input.RiskLevel = "medium"
			default:
				input.ReviewStatus = "pending"
			}
			input.ReviewComment = outcome.Comment
		}
	}

	return input, nil
}

// Preview constructs the CreateInstanceRequest without sending it to Feishu (Dry-Run).
func (s *Service) Preview(ctx context.Context, recordID string) (CreateInstanceRequest, error) {
	input, err := s.PrepareInput(ctx, recordID)
	if err != nil {
		return CreateInstanceRequest{}, err
	}
	return BuildCreateInstanceRequest(s.options.ApprovalCode, input)
}

// Push builds and sends the approval instance creation request to Feishu.
func (s *Service) Push(ctx context.Context, recordID string) (CreateInstanceResponse, error) {
	if s.client == nil {
		return CreateInstanceResponse{}, fmt.Errorf("approval client is not initialized")
	}
	req, err := s.Preview(ctx, recordID)
	if err != nil {
		return CreateInstanceResponse{}, err
	}
	return s.client.CreateInstance(ctx, req)
}
