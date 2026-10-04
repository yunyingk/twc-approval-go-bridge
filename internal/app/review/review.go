// Package review coordinates audits, independently of Feishu, Seal and model SDKs.
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

var ErrStale = errors.New("review result belongs to a superseded source revision")

type Source interface {
	ReadDetail(context.Context, string) (core.Detail, error)
	ReadLedgerEntry(context.Context, string) (core.LedgerEntry, error)
	FindInvoiceCandidates(context.Context, string) ([]dupcheck.Invoice, error)
}

// ScopedSource lets a configured adapter reject another source's saved outcome
// before reading attachments or records from its current tenant.
type ScopedSource interface {
	ReviewLogicalID(string) string
}
type Reviewer interface {
	Review(context.Context, core.Request) (core.Submission, error)
}
type Store interface {
	Begin(context.Context, core.Request) (core.Attempt, bool, error)
	Update(context.Context, string, func(*core.Attempt) error) (core.Attempt, error)
}
type Writer interface {
	WriteReviewResult(context.Context, core.Request, core.Outcome) error
}
type Options struct {
	Provider, ProviderVersion, RulesVersion string
	Versioned                               bool
	Store                                   Store
	Writer                                  Writer
}
type Service struct {
	source   Source
	reviewer Reviewer
	options  Options
}

func New(source Source, reviewer Reviewer, options Options) (*Service, error) {
	if source == nil || reviewer == nil || options.Provider == "" {
		return nil, fmt.Errorf("review source, provider and reviewer are required")
	}
	return &Service{source, reviewer, options}, nil
}

// Prepare reads every attachment and its written ledger result before any
// external submission. One reimbursement has one snapshot with many invoices.
func (s *Service) Prepare(ctx context.Context, recordID string) (core.Request, error) {
	if strings.TrimSpace(recordID) == "" {
		return core.Request{}, fmt.Errorf("source record ID is required")
	}
	detail, err := s.source.ReadDetail(ctx, recordID)
	if err != nil {
		return core.Request{}, err
	}
	if detail.RecordID != recordID || len(detail.Files) == 0 {
		return core.Request{}, fmt.Errorf("source detail does not match record or has no attachments")
	}
	invoices := make([]aggregate.Invoice, 0, len(detail.Files))
	for _, file := range detail.Files {
		entry, err := s.source.ReadLedgerEntry(ctx, recordID+":"+file.Token)
		if err != nil {
			return core.Request{}, fmt.Errorf("load invoice ledger for attachment: %w", err)
		}
		var candidates []dupcheck.Invoice
		if entry.Facts.Number != "" {
			candidates, err = s.source.FindInvoiceCandidates(ctx, entry.Facts.Number)
			if err != nil {
				return core.Request{}, fmt.Errorf("search invoice ledger: %w", err)
			}
		}
		invoices = append(invoices, aggregate.Invoice{FileToken: file.Token, LedgerRecordID: entry.RecordID, Attachment: file.Attachment, Recognition: entry.Recognition, Facts: entry.Facts, Candidates: candidates})
	}
	batch, err := aggregate.Build(detail.DocumentID, detail.DocumentSN, recordID, detail.StartTime, invoices)
	if err != nil {
		return core.Request{}, err
	}
	r := core.Request{Document: batch, LogicalID: detail.DocumentID, Provider: s.options.Provider, ProviderVersion: s.options.ProviderVersion, RulesVersion: s.options.RulesVersion, Context: detail.Context, Transactions: detail.Transactions}
	r.Revision, err = core.Fingerprint(r)
	if err != nil {
		return core.Request{}, err
	}
	if s.options.Versioned {
		r.Document.DocumentID = r.LogicalID + ":v:" + r.Revision
	}
	return r, nil
}

func (s *Service) Submit(ctx context.Context, recordID string) (core.Submission, error) {
	r, err := s.Prepare(ctx, recordID)
	if err != nil {
		return core.Submission{}, err
	}
	if s.options.Store != nil {
		attempt, created, err := s.options.Store.Begin(ctx, r)
		if err != nil {
			return core.Submission{}, err
		}
		if !created {
			if attempt.Submission == nil {
				return core.Submission{}, fmt.Errorf("review attempt %s has state %s; reconcile it before resubmitting", r.Document.DocumentID, attempt.State)
			}
			if err := s.deliver(ctx, attempt); err != nil {
				return core.Submission{}, err
			}
			return *attempt.Submission, nil
		}
	}
	result, callErr := s.reviewer.Review(ctx, r)
	if callErr == nil {
		callErr = validateSubmission(r, result)
	}
	if s.options.Store != nil {
		// A lost response may mean the provider accepted the request. Never auto-resubmit.
		attempt, saveErr := s.options.Store.Update(context.WithoutCancel(ctx), r.Document.DocumentID, func(a *core.Attempt) error {
			if a.State == "completed" {
				return nil
			}
			if callErr != nil {
				a.State = "unknown"
				if errors.Is(callErr, core.ErrRequestRejected) {
					a.State = "failed"
				}
				return nil
			}
			a.State, a.Submission = result.Status, &result
			return nil
		})
		if saveErr != nil {
			return core.Submission{}, fmt.Errorf("persist review attempt: %w", saveErr)
		}
		if callErr != nil && attempt.Submission == nil {
			return core.Submission{}, fmt.Errorf("review %s: %w", r.Document.DocumentID, callErr)
		}
		if err := s.deliver(ctx, attempt); err != nil {
			return core.Submission{}, err
		}
		return *attempt.Submission, nil
	}
	if callErr != nil {
		return core.Submission{}, callErr
	}
	if result.Outcome != nil && s.options.Writer != nil {
		if err := s.options.Writer.WriteReviewResult(ctx, r, *result.Outcome); err != nil {
			return core.Submission{}, err
		}
	}
	return result, nil
}

// Complete is called only after the transport verifies the provider response.
// Outcomes are durably saved before writeback, including late or duplicate results.
func (s *Service) Complete(ctx context.Context, documentID, provider string, outcome core.Outcome) error {
	if s.options.Store == nil {
		return fmt.Errorf("review completion requires a persistent store")
	}
	if err := outcome.Validate(); err != nil {
		return err
	}
	attempt, err := s.options.Store.Update(ctx, documentID, func(a *core.Attempt) error {
		if a.Request.Provider != provider {
			return fmt.Errorf("review provider does not match attempt")
		}
		if a.Submission != nil && a.Submission.Outcome != nil {
			old, _ := json.Marshal(a.Submission.Outcome)
			next, _ := json.Marshal(outcome)
			if string(old) != string(next) {
				return fmt.Errorf("conflicting review result")
			}
			return nil
		}
		if a.Submission == nil {
			a.Submission = &core.Submission{DocumentID: documentID}
		}
		a.State, a.Submission.Status, a.Submission.Outcome = "completed", "completed", &outcome
		return nil
	})
	if err != nil {
		return err
	}
	return s.deliver(ctx, attempt)
}

func (s *Service) deliver(ctx context.Context, attempt core.Attempt) error {
	if attempt.Delivered || attempt.Submission == nil || attempt.Submission.Outcome == nil || s.options.Writer == nil {
		return nil
	}
	if source, ok := s.source.(ScopedSource); ok && source.ReviewLogicalID(attempt.Request.Document.RecordID) != attempt.Request.LogicalID {
		return ErrStale
	}
	// In-flight work keeps its original implementation and rules, even after
	// configuration changes. Only changed business evidence invalidates its result.
	pinned := *s
	pinned.options.Provider = attempt.Request.Provider
	pinned.options.ProviderVersion = attempt.Request.ProviderVersion
	pinned.options.RulesVersion = attempt.Request.RulesVersion
	current, err := pinned.Prepare(ctx, attempt.Request.Document.RecordID)
	if err != nil {
		return err
	}
	if current.Revision != attempt.Request.Revision {
		return ErrStale
	}
	if err := s.options.Writer.WriteReviewResult(ctx, attempt.Request, *attempt.Submission.Outcome); err != nil {
		return err
	}
	_, err = s.options.Store.Update(ctx, attempt.Request.Document.DocumentID, func(a *core.Attempt) error { a.Delivered = true; return nil })
	return err
}

type RetryStore interface {
	PendingReviews(context.Context) ([]core.Attempt, error)
}

// RetryWritebacks resumes delivery without calling either audit provider again.
func (s *Service) RetryWritebacks(ctx context.Context) error {
	store, ok := s.options.Store.(RetryStore)
	if !ok || s.options.Writer == nil {
		return nil
	}
	attempts, err := store.PendingReviews(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, attempt := range attempts {
		if err := s.deliver(ctx, attempt); err != nil && !errors.Is(err, ErrStale) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func validateSubmission(r core.Request, result core.Submission) error {
	if result.DocumentID != r.Document.DocumentID {
		return fmt.Errorf("review submission document ID does not match request")
	}
	if result.Status == "pending" && result.Outcome == nil {
		return nil
	}
	if result.Status != "completed" || result.Outcome == nil {
		return fmt.Errorf("reviewer returned an invalid submission state")
	}
	return result.Outcome.Validate()
}
