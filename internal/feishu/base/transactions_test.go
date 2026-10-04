package base

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transactionTransport struct {
	relation         json.RawMessage
	wrongTarget      bool
	multiple         bool
	wrongAmountType  bool
	denyTransaction  bool
	values           map[string]json.RawMessage
	transactionReads int
}

func (t *transactionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var payload any
	switch {
	case strings.HasSuffix(req.URL.Path, "/tenant_access_token/internal") && req.Method == http.MethodPost:
		payload = map[string]any{"code": 0, "tenant_access_token": "app-token"}
	default:
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer app-token" {
			return nil, fmt.Errorf("transaction source must use app identity and GET only")
		}
		switch {
		case strings.HasSuffix(req.URL.Path, "/details/fields"):
			target := "transactions"
			if t.wrongTarget {
				target = "old-transactions"
			}
			payload = map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"field_id": "relation-id", "field_name": "Renamed relation", "type": 21, "property": map[string]any{"table_id": target, "multiple": t.multiple}}}}}
		case strings.HasSuffix(req.URL.Path, "/details/records/detail"):
			payload = map[string]any{"code": 0, "data": map[string]any{"record": map[string]any{"record_id": "detail", "fields": map[string]any{"Renamed relation": t.relation}}}}
		case strings.HasSuffix(req.URL.Path, "/transactions/fields"):
			var fields []any
			for semantic := range transactionBinding().Fields {
				fieldType := 1
				switch semantic {
				case "original_amount", "booked_amount_cny":
					fieldType = 2
				case "original_currency":
					fieldType = 3
				case "transaction_time":
					fieldType = 5
				}
				if semantic == "original_amount" && t.wrongAmountType {
					fieldType = 3
				}
				fields = append(fields, map[string]any{"field_id": semantic + "-id", "field_name": "Renamed " + semantic, "type": fieldType})
			}
			payload = map[string]any{"code": 0, "data": map[string]any{"items": fields}}
		case strings.Contains(req.URL.Path, "/transactions/records/"):
			t.transactionReads++
			if t.denyTransaction {
				return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			id := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			fields := make(map[string]json.RawMessage)
			for semantic, value := range t.values {
				fields["Renamed "+semantic] = value
			}
			payload = map[string]any{"code": 0, "data": map[string]any{"record": map[string]any{"record_id": id, "fields": fields}}}
		default:
			return nil, fmt.Errorf("unexpected read: %s", req.URL.Path)
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
}

func transactionBinding() TransactionConfig {
	fields := map[string]string{}
	for _, semantic := range []string{"transaction_id", "merchant", "transaction_time", "original_amount", "original_currency", "booked_amount_cny"} {
		fields[semantic] = semantic + "-id"
	}
	return TransactionConfig{BaseToken: "base", TableID: "transactions", RelationFieldID: "relation-id", Fields: fields}
}

func transactionFixture(t *testing.T) (*ReviewSource, *transactionTransport) {
	t.Helper()
	s, err := NewReviewSource("app", "secret", "base", "details", "attachment", "detail-id", "ledger", map[string]string{"source_key": "source", "raw_json": "raw", "invoice_number": "number"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.WithTransactions(transactionBinding()); err != nil {
		t.Fatal(err)
	}
	transport := &transactionTransport{relation: json.RawMessage(`[{"record_ids":["recA"],"table_id":"transactions","text":"NOT-A-RECORD-ID"}]`), values: map[string]json.RawMessage{
		"transaction_id": json.RawMessage(`"BANK-001"`), "merchant": json.RawMessage(`"Shop"`), "transaction_time": json.RawMessage(`1735689600000`),
		"original_amount": json.RawMessage(`"9007199254740993.01"`), "original_currency": json.RawMessage(`"usd"`), "booked_amount_cny": json.RawMessage(`700.10`),
	}}
	s.client.httpClient.Transport = transport
	return s, transport
}

func TestReadTransactionsFollowsRecordIDsAndStableFieldIDs(t *testing.T) {
	s, transport := transactionFixture(t)
	evidence, err := s.ReadTransactions(context.Background(), "detail")
	if err != nil {
		t.Fatal(err)
	}
	if transport.transactionReads != 1 || len(evidence.Issues) != 0 || evidence.Source != "feishu:base:transactions" || len(evidence.Transactions) != 1 {
		t.Fatalf("wrong source evidence: %#v", evidence)
	}
	got := evidence.Transactions[0]
	if got.RecordID != "recA" || got.TransactionID != "BANK-001" || got.OriginalAmount != "9007199254740993.01" || got.OriginalCurrency != "USD" || got.BookedAmountCNY != "700.10" || got.OccurredAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("payment facts changed: %#v", got)
	}
}

func TestEmptyRelationBecomesExplicitEvidenceWithoutReadingPayments(t *testing.T) {
	s, transport := transactionFixture(t)
	transport.relation = json.RawMessage(`[{"table_id":"transactions","text_arr":[],"type":"text"}]`)
	evidence, err := s.ReadTransactions(context.Background(), "detail")
	if err != nil || transport.transactionReads != 0 || len(evidence.Transactions) != 0 || len(evidence.Issues) != 1 || evidence.Issues[0].Code != "missing_transaction_relation" {
		t.Fatalf("empty native relation mishandled: %#v %v", evidence, err)
	}
}

func TestPaymentReadErrorsAndWrongBindingsDoNotBecomeMissingFacts(t *testing.T) {
	for _, scenario := range []string{"wrong target", "wrong amount type", "permission denied"} {
		t.Run(scenario, func(t *testing.T) {
			s, transport := transactionFixture(t)
			switch scenario {
			case "wrong target":
				transport.wrongTarget = true
			case "wrong amount type":
				transport.wrongAmountType = true
			case "permission denied":
				transport.denyTransaction = true
			}
			if evidence, err := s.ReadTransactions(context.Background(), "detail"); err == nil || evidence != nil {
				t.Fatal("configuration or API error became a usable partial snapshot")
			}
		})
	}
}

func TestPaymentAmountsKeepZeroAndNegativeButRejectAmbiguousInput(t *testing.T) {
	for _, value := range []string{`"0"`, `"-12.50"`, `"1,23"`, `null`} {
		t.Run(value, func(t *testing.T) {
			s, transport := transactionFixture(t)
			transport.values["original_amount"] = json.RawMessage(value)
			evidence, err := s.ReadTransactions(context.Background(), "detail")
			if err != nil {
				t.Fatal(err)
			}
			got := evidence.Transactions[0]
			if value == `"0"` || value == `"-12.50"` {
				if got.OriginalAmount != strings.Trim(value, `"`) || len(evidence.Issues) != 0 {
					t.Fatal("valid signed decimal or zero was lost")
				}
			} else if got.OriginalAmount != "" || len(evidence.Issues) != 1 {
				t.Fatal("invalid or missing amount became a fact")
			}
		})
	}
}

func TestMultipleLinksRetainEveryRecordAndFlagSingleRelationViolation(t *testing.T) {
	s, transport := transactionFixture(t)
	transport.relation = json.RawMessage(`[{"record_ids":["recB","recA","recB"],"table_id":"transactions"}]`)
	evidence, err := s.ReadTransactions(context.Background(), "detail")
	if err != nil || len(evidence.Transactions) != 2 || transport.transactionReads != 2 || len(evidence.Issues) != 1 || evidence.Transactions[0].RecordID != "recA" || evidence.Transactions[1].RecordID != "recB" {
		t.Fatalf("links lost or merged: %#v %v", evidence, err)
	}
	transport.multiple = true
	evidence, err = s.ReadTransactions(context.Background(), "detail")
	if err != nil || len(evidence.Issues) != 0 || len(evidence.Transactions) != 2 {
		t.Fatal("valid multi-relation was interpreted as one payment")
	}
}

func TestMalformedCurrencyAndTimeStayIssuesWithoutInventedFacts(t *testing.T) {
	s, transport := transactionFixture(t)
	transport.values["original_currency"] = json.RawMessage(`"人民币"`)
	transport.values["transaction_time"] = json.RawMessage(`"unknown-date"`)
	evidence, err := s.ReadTransactions(context.Background(), "detail")
	if err != nil {
		t.Fatal(err)
	}
	got := evidence.Transactions[0]
	if got.OriginalCurrency != "" || got.OccurredAt != "" || len(evidence.Issues) != 2 {
		t.Fatal("unmapped currency or invalid time became a guessed fact")
	}
	if evidence.Issues[0].RawValue != "人民币" || evidence.Issues[1].RawValue != "unknown-date" {
		t.Fatal("malformed original evidence was lost")
	}
}

func TestRelatedRecordIDsRejectUnsupportedAndWrongTargetData(t *testing.T) {
	for _, raw := range []string{`{"table_id":"other","record_ids":["recA"]}`, `{"text":"BANK-001"}`, `42`, `{}`, `[{"table_id":"transactions","text_arr":["BANK-001"]}]`} {
		if _, err := relatedRecordIDs(json.RawMessage(raw), "transactions"); err == nil {
			t.Fatalf("invalid relation accepted: %s", raw)
		}
	}
	ids, err := relatedRecordIDs(json.RawMessage(`{"link_record_ids":["recB","recA","recB"]}`), "transactions")
	if err != nil || len(ids) != 2 || ids[0] != "recA" {
		t.Fatal("native search relation did not decode")
	}
}
