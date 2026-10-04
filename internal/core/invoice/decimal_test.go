package invoice

import (
	"encoding/json"
	"testing"
)

func TestDecimalPreservesPrecisionAndRejectsAmbiguousGrouping(t *testing.T) {
	for _, item := range []struct{ input, expected string }{{"0", "0"}, {"1,200.50", "1200.50"}, {"9007199254740993.01", "9007199254740993.01"}} {
		value, ok := Decimal(item.input)
		if !ok || value.String() != item.expected {
			t.Fatalf("decimal %s: %s", item.input, value)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != item.expected {
			t.Fatal("amount lost precision during JSON encoding")
		}
	}
	for _, invalid := range []string{"", "1,23", "NaN", "00", "$10", "1.234,56"} {
		if _, ok := Decimal(invalid); ok {
			t.Fatalf("accepted %q", invalid)
		}
	}
}
