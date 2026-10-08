//go:build !no_anthropic

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

func TestModelRulesResolveFromMainFileAndKeepContentVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rules", "audit.json")
	raw := `{"version":"v1","instructions":"Use supplied facts"}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ConfigFile: filepath.Join(dir, "config.toml"), ReviewRulesFile: "rules/audit.json", ReviewModelAPIKey: "test-key", ReviewModelName: "test-model"}
	_, version, provider, err := newModelReview(cfg)
	if err != nil || version != "v1" {
		t.Fatalf("relative rules file failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("\n"+raw+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, reformatted, err := newModelReview(cfg)
	if err != nil || provider != reformatted {
		t.Fatal("formatting changed the review content version")
	}
	cfg.ReviewRulesFile = "rules/missing.json"
	if _, _, _, err := newModelReview(cfg); err == nil {
		t.Fatal("missing selected rules accepted")
	}
}
