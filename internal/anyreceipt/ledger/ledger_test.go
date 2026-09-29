package ledger

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/anyreceipt/flow"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

type fakeStore struct {
	key    string
	fields map[string]any
	calls  int
}

func (s *fakeStore) ReadTextField(_ context.Context, base, table, record, field string) (string, error) {
	if base != "base" || table != "detail" || record != "rec" || field != "detail_id_field" {
		return "", context.Canceled
	}
	return "DETAIL-001", nil
}

func (s *fakeStore) UpsertLedgerRecord(_ context.Context, base, table, keyField, key string, fields map[string]any) (string, bool, error) {
	if base != "base" || table != "ledger" || keyField != "source_field" {
		return "", false, context.Canceled
	}
	s.key, s.fields = key, fields
	s.calls++
	return "ledger-record", s.calls == 1, nil
}

func TestHandleMapsRecognitionAndKeepsRawJSON(t *testing.T) {
	store := &fakeStore{}
	h, err := New(Config{BaseToken: "base", SourceTableID: "detail", SourceDetailFieldID: "detail_id_field", TableID: "ledger", Fields: map[string]string{
		SourceKey: "source_field", RawJSON: "raw_field", DetailID: "detail_field", Relation: "relation_field", UniqueKey: "unique_field", Title: "title_field", Number: "number_field", ReceiptType: "type_field", BusinessCategory: "category_field", Seller: "seller_field", Buyer: "buyer_field", Currency: "currency_field", Pretax: "pretax_field", Tax: "tax_field", TaxRate: "tax_rate_field", Total: "total_field", IssueDate: "date_field", Country: "country_field", AISummary: "summary_field",
	}}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"traceId":"TRACE-7","outputs":{"title":"Dinner receipt","Number":"INV-7","type":"invoice","TypeofBill":"meals","Seller":"Example Ltd","Buyer":"Employee","currency":"usd","amountwithouttax":"1,200.50","tax":"120","taxrate":"10%","total":"1,320.50","DateofInssuance":"2026-09-29","country":"US","unknown_vendor_field":"kept"},"summary":"receipt"}`)
	result := flow.Result{BaseToken: "base", TableID: "detail", RecordID: "rec", FileToken: "file", Recognition: invoice.Recognition{Raw: raw, Outputs: map[string]json.RawMessage{
		"title": json.RawMessage(`"Dinner receipt"`), "Number": json.RawMessage(`"INV-7"`), "type": json.RawMessage(`"invoice"`), "TypeofBill": json.RawMessage(`"meals"`), "Seller": json.RawMessage(`"Example Ltd"`), "Buyer": json.RawMessage(`"Employee"`), "currency": json.RawMessage(`"usd"`), "amountwithouttax": json.RawMessage(`"1,200.50"`), "tax": json.RawMessage(`"120"`), "taxrate": json.RawMessage(`"10%"`), "total": json.RawMessage(`"1,320.50"`), "DateofInssuance": json.RawMessage(`"2026-09-29"`), "country": json.RawMessage(`"US"`),
	}, Summary: "receipt"}}
	if err := h.Handle(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if store.key != "rec:file" || store.fields["raw_field"] != string(raw) || store.fields["detail_field"] != "DETAIL-001" {
		t.Fatalf("source mapping is wrong: key=%q fields=%v", store.key, store.fields)
	}
	links, ok := store.fields["relation_field"].([]string)
	if !ok || len(links) != 1 || links[0] != "rec" {
		t.Fatalf("relation mapping is wrong: %v", store.fields["relation_field"])
	}
	if store.fields["title_field"] != "Dinner receipt" || store.fields["number_field"] != "INV-7" || store.fields["type_field"] != "invoice" || store.fields["category_field"] != "meals" || store.fields["seller_field"] != "Example Ltd" || store.fields["buyer_field"] != "Employee" || store.fields["currency_field"] != "USD" || store.fields["pretax_field"] != 1200.5 || store.fields["tax_field"] != 120.0 || store.fields["tax_rate_field"] != "10%" || store.fields["total_field"] != 1320.5 || store.fields["country_field"] != "US" || store.fields["summary_field"] != "receipt" {
		t.Fatalf("OCR mapping is wrong: %v", store.fields)
	}
	date := time.Date(2026, 9, 29, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)).UnixMilli()
	if store.fields["date_field"] != date || store.fields["unique_field"] != "TRACE-7" {
		t.Fatalf("date or unique key is wrong: %v", store.fields)
	}
}

func TestHandleUsesStableUniqueKeyWhenTraceIDAbsent(t *testing.T) {
	store := &fakeStore{}
	h, err := New(Config{BaseToken: "base", SourceTableID: "detail", TableID: "ledger", Fields: map[string]string{
		SourceKey: "source_field", RawJSON: "raw_field", UniqueKey: "unique_field", AISummary: "summary_field",
	}}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := flow.Result{BaseToken: "base", TableID: "detail", RecordID: "rec", FileToken: "file", Recognition: invoice.Recognition{
		Raw: json.RawMessage(`{"outputs":{},"summary":"model receipt"}`), Summary: "model receipt",
	}}
	if err := h.Handle(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	key := store.fields["unique_field"]
	if value, ok := key.(string); !ok || !strings.HasPrefix(value, "OCR-") || store.fields["summary_field"] != "model receipt" {
		t.Fatalf("model fallback mapping is wrong: %v", store.fields)
	}
	if err := h.Handle(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if store.fields["unique_field"] != key {
		t.Fatalf("fallback unique key changed: first=%v second=%v", key, store.fields["unique_field"])
	}
}

func TestHandleRejectsWrongSource(t *testing.T) {
	store := &fakeStore{}
	h, err := New(Config{BaseToken: "base", SourceTableID: "detail", TableID: "ledger", Fields: map[string]string{SourceKey: "source", RawJSON: "raw"}}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(context.Background(), flow.Result{BaseToken: "different", TableID: "detail", RecordID: "rec", FileToken: "file"}); err == nil {
		t.Fatal("wrong Base was accepted")
	}
	if store.calls != 0 {
		t.Fatal("wrong Base was written")
	}
}
