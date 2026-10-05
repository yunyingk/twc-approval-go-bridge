package review

import "sort"

// TransactionEvidence records the linked payment facts in the common snapshot.
// Source is an opaque source scope; providers do not interpret platform APIs.
// Amounts are exact decimal strings, and missing values remain empty.
type TransactionEvidence struct {
	Source          string          `json:"source"`
	LinkedRecordIDs []string        `json:"linked_record_ids"`
	Transactions    []Transaction   `json:"transactions"`
	Issues          []EvidenceIssue `json:"issues,omitempty"`
}

type Transaction struct {
	RecordID         string `json:"record_id"`
	TransactionID    string `json:"transaction_id"`
	Merchant         string `json:"merchant"`
	OccurredAt       string `json:"occurred_at"`
	OriginalAmount   string `json:"original_amount"`
	OriginalCurrency string `json:"original_currency"`
	BookedAmountCNY  string `json:"booked_amount_cny,omitempty"`
	Country          string `json:"country,omitempty"`
	TransactionType  string `json:"transaction_type,omitempty"`
	Status           string `json:"status,omitempty"`
}

// EvidenceIssue describes missing or malformed input, not an approval rule.
// Invalid input stays available as evidence without becoming a guessed fact.
type EvidenceIssue struct {
	Code     string `json:"code"`
	RecordID string `json:"record_id,omitempty"`
	Field    string `json:"field,omitempty"`
	RawValue string `json:"raw_value,omitempty"`
}

// CanonicalTransactionEvidence gives private audit snapshots the same ordering
// as the AI fingerprint, without changing the caller's linked evidence.
func CanonicalTransactionEvidence(evidence *TransactionEvidence) *TransactionEvidence {
	return canonicalTransactions(evidence)
}

func canonicalTransactions(evidence *TransactionEvidence) *TransactionEvidence {
	if evidence == nil {
		return nil
	}
	result := *evidence
	result.LinkedRecordIDs = append([]string(nil), evidence.LinkedRecordIDs...)
	result.Transactions = append([]Transaction(nil), evidence.Transactions...)
	result.Issues = append([]EvidenceIssue(nil), evidence.Issues...)
	sort.Strings(result.LinkedRecordIDs)
	sort.Slice(result.Transactions, func(i, j int) bool { return result.Transactions[i].RecordID < result.Transactions[j].RecordID })
	sort.Slice(result.Issues, func(i, j int) bool {
		a, b := result.Issues[i], result.Issues[j]
		if a.RecordID != b.RecordID {
			return a.RecordID < b.RecordID
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.RawValue < b.RawValue
	})
	return &result
}
