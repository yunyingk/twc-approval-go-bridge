package receiptcompat

import (
	"encoding/json"
	"testing"
)

func TestLegacyAndStandardFactsAgree(t *testing.T) {
	legacy, err := Decode(json.RawMessage(`{"data":{"outputs":{"Number":"N1","total":"12.30","currency":"USD","Seller":"shop"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	standard, err := Decode(json.RawMessage(`{"facts":{"number":"N1","total":"12.30","currency":"USD","seller":"shop"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if *legacy.Facts != *standard.Facts {
		t.Fatal("provider formats produced different facts")
	}
	if _, err := Decode(json.RawMessage(`{"multiple_receipts":true,"facts":{"number":"N1"}}`)); err == nil {
		t.Fatal("multi-receipt output was silently merged")
	}
}
