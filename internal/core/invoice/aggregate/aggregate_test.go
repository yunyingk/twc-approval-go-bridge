package aggregate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

func TestBuildOrdersMultipleInvoicesAndFindsCandidates(t *testing.T) {
	makeInvoice := func(token string) Invoice {
		return Invoice{FileToken: token, LedgerRecordID: "ledger-" + token,
			Recognition: invoice.Recognition{Raw: json.RawMessage(`{"outputs":{}}`)},
			Facts:       dupcheck.Invoice{SourceKey: "detail:" + token, Number: "N-1", Seller: "Seller", Type: "invoice"}}
	}
	second, first := makeInvoice("b"), makeInvoice("a")
	second.Candidates = []dupcheck.Invoice{{RecordID: "old", SourceKey: "other:c", Number: "N-1", Seller: "Seller", Type: "invoice"}}
	got, err := Build("document", "sn", "detail", time.Unix(100, 0), []Invoice{second, first})
	if err != nil || len(got.Invoices) != 2 || got.Invoices[0].FileToken != "a" || len(got.Findings) != 1 {
		t.Fatalf("unexpected aggregation: %#v, %v", got, err)
	}
	if _, err := Build("document", "sn", "detail", time.Unix(100, 0), []Invoice{first, first}); err == nil {
		t.Fatal("duplicate attachment tokens must fail")
	}
}
