//go:build !no_anthropic

package review

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentRulesFileRejectsInvalidPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	valid := `{"version":"v1","instructions":"Use supplied facts","required_context":["merchant"],"allow_automatic_decision":false}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(path)
	if err != nil || rules.Version != "v1" || len(rules.RequiredContext) != 1 || rules.AllowAutomaticDecision {
		t.Fatalf("rules lost policy: %v", err)
	}
	for _, raw := range []string{`{}`, valid + `{}`, `{"version":"v1","instructions":"policy","typo":true}`, `{"version":"v1","instructions":`, `{"version":"v1","instructions":null}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRules(path); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
