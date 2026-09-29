package dupcheck

import "strings"

// Invoice is the small set of ledger facts used to find possible prior use.
// The caller supplies records from the configured invoice ledger only.
type Invoice struct {
	RecordID  string
	SourceKey string
	Number    string
	Seller    string
	Type      string
}

type Finding struct {
	CurrentSourceKey string
	PriorRecordID    string
	PriorSourceKey   string
	Code             string
}

// Compare produces evidence for AI review; it never makes an approval decision.
func Compare(current Invoice, candidates []Invoice) []Finding {
	number := normalize(current.Number)
	if number == "" {
		return nil
	}
	findings := make([]Finding, 0)
	for _, prior := range candidates {
		if prior.SourceKey == current.SourceKey || normalize(prior.Number) != number {
			continue
		}
		code := "same_number_needs_review"
		if normalize(prior.Seller) != "" && normalize(prior.Seller) == normalize(current.Seller) &&
			normalize(prior.Type) != "" && normalize(prior.Type) == normalize(current.Type) {
			code = "same_seller_number_type"
		}
		findings = append(findings, Finding{
			CurrentSourceKey: current.SourceKey, PriorRecordID: prior.RecordID,
			PriorSourceKey: prior.SourceKey, Code: code,
		})
	}
	return findings
}

func normalize(value string) string {
	return strings.ToUpper(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
}
