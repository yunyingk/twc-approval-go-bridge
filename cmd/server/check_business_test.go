package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
)

type fakeInspector struct {
	schemas map[string]map[string]base.TableField
	calls   []string
	err     error
}

func (f *fakeInspector) InspectFields(_ context.Context, base, table string) (map[string]base.TableField, error) {
	f.calls = append(f.calls, base+":"+table)
	return f.schemas[table], f.err
}

func checkFixture() (*config.BusinessProfile, *fakeInspector) {
	p := &config.BusinessProfile{Name: "test", Tables: config.BusinessTables{
		Transactions:         config.TableBinding{BaseToken: "base", TableID: "transactions", Fields: map[string]string{"transaction_id": "transaction", "detail_relation": "transaction-link"}},
		ReimbursementDetails: config.TableBinding{BaseToken: "base", TableID: "details", Fields: map[string]string{"attachment": "attachment", "transaction_relation": "detail-link", "invoice_relation": "invoice-link"}},
		InvoiceLedger:        config.TableBinding{BaseToken: "base", TableID: "ledger", Fields: map[string]string{"source_key": "source", "raw_json": "raw", "relation": "ledger-link"}},
	}, Review: config.ReviewSettings{ResultFields: map[string]string{"decision": "decision"}}}
	f := &fakeInspector{schemas: map[string]map[string]base.TableField{
		"transactions": {"transaction": {Type: 1}, "transaction-link": {Type: 21, RelatedTableID: "details"}},
		"details":      {"attachment": {Type: 17}, "decision": {Type: 1}, "detail-link": {Type: 21, RelatedTableID: "transactions"}, "invoice-link": {Type: 21, RelatedTableID: "ledger"}},
		"ledger":       {"source": {Type: 1}, "raw": {Type: 1}, "ledger-link": {Type: 21, RelatedTableID: "details"}},
	}}
	return p, f
}

func TestBusinessCheckInspectsAllRolesWithoutPartialSuccessOutput(t *testing.T) {
	p, f := checkFixture()
	var output bytes.Buffer
	if err := checkBusiness(context.Background(), p, f, &output); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("inspected %d tables, want 3", len(f.calls))
	}
	var result struct {
		Tables []checkedTable `json:"tables"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || len(result.Tables) != 3 {
		t.Fatalf("invalid complete summary: %v", err)
	}
	delete(f.schemas["ledger"], "raw")
	output.Reset()
	if err := checkBusiness(context.Background(), p, f, &output); err == nil || output.Len() != 0 {
		t.Fatal("missing ledger evidence accepted or partial success printed")
	}
}

func TestBusinessCheckRejectsWrongAIColumnAndCopiedRelationships(t *testing.T) {
	for _, scenario := range []string{"AI select field", "attachment text field", "link to old ledger", "no app permission"} {
		t.Run(scenario, func(t *testing.T) {
			p, f := checkFixture()
			switch scenario {
			case "AI select field":
				f.schemas["details"]["decision"] = base.TableField{Type: 3}
			case "attachment text field":
				f.schemas["details"]["attachment"] = base.TableField{Type: 1}
			case "link to old ledger":
				f.schemas["details"]["invoice-link"] = base.TableField{Type: 21, RelatedTableID: "old-ledger"}
			case "no app permission":
				f.err = errors.New("permission denied")
			}
			if err := checkBusiness(context.Background(), p, f, &bytes.Buffer{}); err == nil {
				t.Fatal("invalid online binding accepted")
			}
		})
	}
}
