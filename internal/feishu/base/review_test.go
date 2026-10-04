package base

import (
	"encoding/json"
	"testing"
)

func TestFieldTextFromBitableSearchSegments(t *testing.T) {
	fields := map[string]json.RawMessage{
		"plain":    json.RawMessage(`"ABC"`),
		"segments": json.RawMessage(`[{"text":"AB"},{"text":"-12"}]`),
		"number":   json.RawMessage(`12.5`),
	}
	if fieldText(fields, "plain") != "ABC" || fieldText(fields, "segments") != "AB-12" || fieldText(fields, "number") != "12.5" {
		t.Fatalf("unexpected Bitable field decoding")
	}
}

func TestLedgerCorrectionsOverrideOriginalOCRForReview(t *testing.T) {
	s := &ReviewSource{ledgerFields: map[string]string{"raw_json": "raw", "source_key": "source", "invoice_number": "number", "seller": "seller", "total_amount": "total"}}
	names := map[string]string{"raw": "Raw", "source": "Source", "number": "Number", "seller": "Seller", "total": "Total"}
	raw := `{"outputs":{"Number":"OLD","Seller":"old shop","total":"12"}}`
	encoded, _ := json.Marshal(raw)
	entry, err := s.decodeLedgerEntry(reviewRow{ID: "ledger", Fields: map[string]json.RawMessage{"Raw": encoded, "Source": json.RawMessage(`"rec:file"`), "Number": json.RawMessage(`"NEW"`), "Seller": json.RawMessage(`""`), "Total": json.RawMessage(`20.01`)}}, names)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Recognition.Facts.Number != "NEW" || entry.Recognition.Facts.Total != "20.01" || entry.Recognition.Facts.Seller != "" || entry.Facts.Number != "NEW" || string(entry.Recognition.Raw) != raw {
		t.Fatal("review facts disagreed with human correction or original evidence was lost")
	}
}

func TestCandidateEvidencePreservesMappedFacts(t *testing.T) {
	s := &ReviewSource{ledgerFields: map[string]string{"source_key": "source", "invoice_number": "number", "seller": "seller", "receipt_type": "type", "issue_date": "date", "total_amount": "total", "currency": "currency"}}
	names := map[string]string{"source": "Source", "number": "Number", "seller": "Seller", "type": "Type", "date": "Date", "total": "Total", "currency": "Currency"}
	got := s.invoiceFacts(reviewRow{ID: "prior", Fields: map[string]json.RawMessage{
		"Source": json.RawMessage(`"old:file"`), "Number": json.RawMessage(`"INV-1"`),
		"Seller": json.RawMessage(`"Shop"`), "Type": json.RawMessage(`"invoice"`),
		"Date": json.RawMessage(`1790611200000`), "Total": json.RawMessage(`9007199254740993.01`), "Currency": json.RawMessage(`"usd"`),
	}}, names)
	if got.IssueDate != "2026-09-29" || got.Total != "9007199254740993.01" || got.Currency != "USD" || got.Seller != "Shop" {
		t.Fatalf("candidate evidence lost date, precision or currency: %#v", got)
	}
}
