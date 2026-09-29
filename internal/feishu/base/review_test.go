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
