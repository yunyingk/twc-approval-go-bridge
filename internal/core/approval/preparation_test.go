package approval_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

func proofRequest(t *testing.T) review.Request {
	t.Helper()
	r := review.Request{LogicalID: "source:record", Provider: "seal", ProviderVersion: "pin", Context: map[string]string{"reason": "checked reason"},
		Document: aggregate.Document{RecordID: "record", DocumentSN: "document", StartTime: time.Now(),
			Findings: []dupcheck.Finding{{PriorRecordID: "prior-b", Code: "same_number_needs_review"}, {PriorRecordID: "prior-a", Code: "same_seller_number_type"}}},
		Transactions: &review.TransactionEvidence{Source: "payments", LinkedRecordIDs: []string{"b", "a"},
			Transactions: []review.Transaction{{RecordID: "b", OriginalAmount: "9007199254740993.12", OriginalCurrency: "USD"}, {RecordID: "a", OriginalAmount: "1.25", OriginalCurrency: "USD"}},
			Issues:       []review.EvidenceIssue{{RecordID: "b", Code: "missing_country"}, {RecordID: "a", Code: "missing_country"}}}}
	for _, token := range []string{"file-b", "file-a"} {
		r.Document.Invoices = append(r.Document.Invoices, aggregate.Invoice{FileToken: token, LedgerRecordID: "ledger-" + token,
			Attachment:  invoice.Attachment{Name: token + ".pdf", ContentType: "application/pdf", Data: []byte("original-" + token), URL: "temporary-url"},
			Recognition: invoice.Recognition{Raw: json.RawMessage(`{"total":9007199254740993.12}`), Facts: &invoice.Facts{Total: "9007199254740993.12", Currency: "USD"}},
			Candidates:  []dupcheck.Invoice{{RecordID: "prior-b", Number: "invoice"}, {RecordID: "prior-a", Number: "invoice"}}})
	}
	var err error
	r.Revision, err = review.Fingerprint(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Document.DocumentID = r.LogicalID + ":v:" + r.Revision
	return r
}

func TestReviewProofKeepsOriginalRevisionAndNormalizesOnlyReadMetadataAndOrder(t *testing.T) {
	r := proofRequest(t)
	before, _ := json.Marshal(r)
	outcome := review.Outcome{Decision: "review", Comment: "checked outcome"}
	first, err := core.NewReviewProof(r, outcome)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("audit generation mutated original AI evidence")
	}
	for _, file := range first.Files {
		content := []byte("original-" + file.Token)
		digest := sha256.Sum256(content)
		if file.Hash != hex.EncodeToString(digest[:]) || file.Size != int64(len(content)) {
			t.Fatal("audit lost exact content proof")
		}
	}
	var reread review.Request
	if err := json.Unmarshal(before, &reread); err != nil {
		t.Fatal(err)
	}
	reread.Document.StartTime = r.Document.StartTime.Add(time.Minute)
	slices.Reverse(reread.Document.Invoices)
	slices.Reverse(reread.Document.Findings)
	for n := range reread.Document.Invoices {
		reread.Document.Invoices[n].Attachment.URL = "new-temporary-url"
		slices.Reverse(reread.Document.Invoices[n].Candidates)
	}
	slices.Reverse(reread.Transactions.LinkedRecordIDs)
	slices.Reverse(reread.Transactions.Transactions)
	slices.Reverse(reread.Transactions.Issues)
	second, err := core.NewReviewProof(reread, outcome)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) || first.Snapshot.Revision != r.Revision || !first.Snapshot.Document.StartTime.IsZero() {
		t.Fatal("read timestamps, temporary URLs or evidence order changed audit facts")
	}
	// A stripped audit is deliberately not an executable AI request. Its revision
	// refers to the original content, whose hashes/lengths are kept separately.
	strippedRevision, err := review.Fingerprint(first.Snapshot)
	if err != nil || strippedRevision == r.Revision {
		t.Fatal("binary-free proof was confused with the complete AI request")
	}
}

func TestReviewProofRejectsChangedAIContentAndMissingOriginals(t *testing.T) {
	for _, change := range []string{"content", "facts", "revision", "missing original"} {
		t.Run(change, func(t *testing.T) {
			r := proofRequest(t)
			switch change {
			case "content":
				r.Document.Invoices[0].Attachment.Data = []byte("different original")
			case "facts":
				r.Document.Invoices[0].Recognition.Facts.Total = "1"
			case "revision":
				r.Revision = "different revision"
			case "missing original":
				r.Document.Invoices[0].Attachment.Data = nil
				// Even a freshly fingerprinted request cannot produce content proof
				// without the originals the current-review check actually read.
				r.Revision, _ = review.Fingerprint(r)
			}
			if _, err := core.NewReviewProof(r, review.Outcome{Decision: "review"}); err == nil {
				t.Fatal("invalid AI evidence produced an audit proof")
			}
		})
	}
}
