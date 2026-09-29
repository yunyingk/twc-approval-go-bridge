package ledger

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/flow"
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
		SourceKey: "source_field", RawJSON: "raw_field", DetailID: "detail_field", UniqueKey: "unique_field", Number: "number_field", Seller: "seller_field", Currency: "currency_field", Pretax: "pretax_field", Total: "total_field", IssueDate: "date_field", Status: "status_field",
	}}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"outputs":{"Number":"INV-7","Seller":"Example Ltd","currency":"usd","amountwithouttax":"1,200.50","total":"1,320.50","DateofInssuance":"2026-09-29","unknown_vendor_field":"kept"},"summary":"receipt"}`)
	result := flow.Result{BaseToken: "base", TableID: "detail", RecordID: "rec", FileToken: "file", Recognition: receipt.Recognition{Raw: raw, Outputs: map[string]json.RawMessage{
		"Number": json.RawMessage(`"INV-7"`), "Seller": json.RawMessage(`"Example Ltd"`), "currency": json.RawMessage(`"usd"`), "amountwithouttax": json.RawMessage(`"1,200.50"`), "total": json.RawMessage(`"1,320.50"`), "DateofInssuance": json.RawMessage(`"2026-09-29"`),
	}}}
	if err := h.Handle(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if store.key != "rec:file" || store.fields["raw_field"] != string(raw) || store.fields["detail_field"] != "DETAIL-001" {
		t.Fatalf("source mapping is wrong: key=%q fields=%v", store.key, store.fields)
	}
	if store.fields["number_field"] != "INV-7" || store.fields["seller_field"] != "Example Ltd" || store.fields["currency_field"] != "USD" || store.fields["pretax_field"] != 1200.5 || store.fields["total_field"] != 1320.5 || store.fields["status_field"] != "已识别" {
		t.Fatalf("OCR mapping is wrong: %v", store.fields)
	}
	date := time.Date(2026, 9, 29, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)).UnixMilli()
	if store.fields["date_field"] != date || !strings.HasPrefix(store.fields["unique_field"].(string), "OCR-") {
		t.Fatalf("date or unique key is wrong: %v", store.fields)
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
