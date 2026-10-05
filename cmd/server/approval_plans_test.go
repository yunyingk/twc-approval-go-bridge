package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type planReviewSource struct{}

func (*planReviewSource) ReviewLogicalID(id string) string { return "feishu:base:details:" + id }
func (s *planReviewSource) ReadDetail(_ context.Context, id string) (review.Detail, error) {
	return review.Detail{DocumentID: s.ReviewLogicalID(id), DocumentSN: id, RecordID: id, StartTime: time.Now(), Files: []review.File{{Token: "file", Attachment: invoice.Attachment{Data: []byte("PRIVATE_BYTES")}}}}, nil
}
func (*planReviewSource) ReadLedgerEntry(_ context.Context, key string) (review.LedgerEntry, error) {
	return review.LedgerEntry{RecordID: "ledger", Recognition: invoice.Recognition{Facts: &invoice.Facts{Total: "9007199254740993.12", Currency: "USD"}, Raw: json.RawMessage(`{"outputs":{}}`)}, Facts: dupcheck.Invoice{SourceKey: key}}, nil
}
func (*planReviewSource) FindInvoiceCandidates(context.Context, string) ([]dupcheck.Invoice, error) {
	return nil, nil
}

type planReviewStore struct{ attempts []review.Attempt }

func (s *planReviewStore) ReviewAttempts(context.Context, string) ([]review.Attempt, error) {
	return s.attempts, nil
}

type planFields struct{ split bool }

func (*planFields) ApprovalSourceScope() string { return "feishu:base:details" }
func (s *planFields) ReadApprovalInputs(_ context.Context, ids []string) ([]app.InputCheck, error) {
	checks := []app.InputCheck{}
	for _, id := range ids {
		project := "project-a"
		if s.split {
			project = id
		}
		checks = append(checks, app.InputCheck{RecordID: id, Issues: []app.InputIssue{}, Row: core.Row{SourceScope: s.ApprovalSourceScope(), SourceVersion: strings.Repeat("c", 64), RecordID: id, GroupValues: map[string]string{"project": project}, Fields: map[string]core.Value{"amount": {Kind: "money", Decimal: "9007199254740993.12", Currency: "USD"}, "text": {Kind: "text", Text: "PRIVATE_FORM_TEXT"}}}})
	}
	return checks, nil
}

type planValidator struct {
	calls  int
	failAt int
}

func (v *planValidator) ValidatePlan(_ context.Context, plan core.Plan) error {
	v.calls++
	if v.calls == v.failAt {
		return errors.New("PRIVATE_TEMPLATE_ERROR")
	}
	if plan.Rows[0].Fields["amount"].Decimal != "9007199254740993.12" || plan.Validate() != nil {
		return errors.New("invalid frozen plan")
	}
	return nil
}

func TestApprovalPlanPreviewValidatesCompleteSelectionAndMasksFormValues(t *testing.T) {
	for _, split := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			source := &planReviewSource{}
			store := &planReviewStore{}
			for _, id := range []string{"b", "a"} {
				request, err := appreview.PreparePinned(context.Background(), source, review.Request{LogicalID: source.ReviewLogicalID(id), Provider: "seal", ProviderVersion: "pin", Document: aggregate.Document{RecordID: id}})
				if err != nil {
					t.Fatal(err)
				}
				store.attempts = append(store.attempts, review.Attempt{Request: request, State: "completed", Submission: &review.Submission{DocumentID: request.Document.DocumentID, Status: "completed", Outcome: &review.Outcome{Decision: "review", Comment: "PRIVATE_COMMENT"}}})
			}
			gate, err := app.NewReviewGate(source, store, "feishu:base:details", "seal", []string{"review"})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := app.NewPreparedSource(&planFields{split: split}, gate)
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := prepared.Inspect(context.Background(), []string{"b", "a"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{ReceiptBaseToken: "base", ReceiptTableID: "details", Business: &config.BusinessProfile{Approval: &config.ApprovalSettings{SubmitterOpenID: "ou_submitter", AllowedAIDecisions: []string{"review"}, GroupBy: []config.ApprovalGroup{{Axis: "project"}}}}}
			want := 1
			if split {
				want = 2
			}
			validator := &planValidator{}
			if fail {
				validator.failAt = want
			}
			plans, issue := previewApprovalPlans(context.Background(), cfg, inspection, approvalPreviewTarget{Scope: "feishu-app:bridge", Template: "template", Version: "version"}, validator)
			if validator.calls != want {
				t.Fatal("not every group was validated")
			}
			if fail {
				if issue != "target_form_invalid" || len(plans) != 0 {
					t.Fatal("partially validated selection exposed as a plan")
				}
			} else if issue != "" || len(plans) != want || len(plans[0].ID) != 36 {
				t.Fatal("validated plan summaries missing")
			}
			raw, _ := json.Marshal(approvalPreflight{Records: inspection.Reviews, Inputs: inspection.Inputs, Plans: plans})
			if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "9007199254740993") {
				t.Fatal("preview exposed private form values or error")
			}
		}
	}
}
