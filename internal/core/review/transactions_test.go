package review

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestUnconfiguredTransactionsPreserveLegacyRevision(t *testing.T) {
	legacy := []byte(`{"LogicalID":"","DocumentSN":"","Provider":"","ProviderVersion":"","RulesVersion":"","Context":null,"Items":[]}`)
	digest := sha256.Sum256(legacy)
	got, err := Fingerprint(Request{})
	if err != nil || got != hex.EncodeToString(digest[:]) {
		t.Fatalf("legacy revision changed: %s %v", got, err)
	}
}

func TestTransactionRevisionIgnoresOrderButIncludesFactsAndSource(t *testing.T) {
	r := Request{Transactions: &TransactionEvidence{Source: "source", LinkedRecordIDs: []string{"b", "a"}, Transactions: []Transaction{{RecordID: "b", OriginalAmount: "9007199254740993.01"}, {RecordID: "a", OriginalAmount: "0"}}}}
	first, _ := Fingerprint(r)
	r.Transactions.LinkedRecordIDs[0], r.Transactions.LinkedRecordIDs[1] = "a", "b"
	r.Transactions.Transactions[0], r.Transactions.Transactions[1] = r.Transactions.Transactions[1], r.Transactions.Transactions[0]
	second, _ := Fingerprint(r)
	if first != second {
		t.Fatal("record ordering created a new revision")
	}
	r.Transactions.Transactions[0].OriginalAmount = "1"
	third, _ := Fingerprint(r)
	if third == first {
		t.Fatal("payment amount change did not change revision")
	}
	r.Transactions.Source = "other-source"
	fourth, _ := Fingerprint(r)
	if fourth == third {
		t.Fatal("payment source change did not change revision")
	}
	r.Transactions.Issues = []EvidenceIssue{{Code: "missing_currency"}}
	fifth, _ := Fingerprint(r)
	if fifth == fourth {
		t.Fatal("evidence completeness change did not change revision")
	}
}
