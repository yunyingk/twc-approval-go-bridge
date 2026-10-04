package main

import (
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestReviewSourceBindingsUseEvidenceFieldsWithoutWorkflowFeedback(t *testing.T) {
	p, _ := checkFixture()
	p.Tables.InvoiceLedger.Fields["invoice_number"] = "number"
	p.Tables.InvoiceLedger.Fields["total_amount"] = "total"
	p.Tables.InvoiceLedger.Fields["ai_summary"] = "derived-summary"
	p.Tables.Transactions.Fields["original_amount"] = "amount"
	p.Tables.Transactions.Fields["locked"] = "locked"
	p.Tables.Transactions.Fields["claim_status"] = "claimed"
	p.Review.IncludeTransactions = true
	bindings := reviewSourceBindings(p)
	if len(bindings) != 2 || bindings[0].Kind != app.InvoiceSource || bindings[1].Kind != app.TransactionSource {
		t.Fatal("wrong source binding set")
	}
	for _, binding := range bindings {
		for _, id := range binding.FieldIDs {
			if id == "derived-summary" || id == "locked" || id == "claimed" || id == "ledger-link" || id == "transaction-link" {
				t.Fatal("workflow writeback can create a review feedback loop")
			}
		}
	}
	p.Review.IncludeTransactions = false
	if len(reviewSourceBindings(p)) != 1 {
		t.Fatal("disabled payment source still watched")
	}
	cfg := config.Config{Business: p, ReceiptTriggerMode: "poll"}
	if reviewChangesEnabled(cfg) {
		t.Fatal("default configuration enabled change events")
	}
	p.Review.ResubmitOnSourceChange = true
	if !reviewChangesEnabled(cfg) {
		t.Fatal("poll recognition hid the required source event listener")
	}
}
