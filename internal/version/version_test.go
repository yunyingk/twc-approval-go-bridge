package version

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionDefaults(t *testing.T) {
	if Version != "0.3.0" {
		t.Fatalf("expected default Version to be 0.3.0, got %s", Version)
	}

	if Short() != "0.3.0" {
		t.Fatalf("expected Short() to be 0.3.0, got %s", Short())
	}

	info := Get()
	if info.Version != "0.3.0" {
		t.Fatalf("expected info.Version to be 0.3.0, got %s", info.Version)
	}
	if info.GoVersion == "" {
		t.Fatal("expected GoVersion to be non-empty")
	}
	if info.Platform == "" {
		t.Fatal("expected Platform to be non-empty")
	}

	str := Full()
	if !strings.HasPrefix(str, "v0.3.0") {
		t.Fatalf("expected Full() to start with v0.3.0, got %s", str)
	}
}

func TestVersionJSON(t *testing.T) {
	data, err := JSON()
	if err != nil {
		t.Fatalf("JSON() returned error: %v", err)
	}

	var parsed Info
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v", err)
	}

	if parsed.Version != "0.3.0" {
		t.Fatalf("unmarshaled version = %s, want 0.3.0", parsed.Version)
	}
}

func TestVersionStringWithMetadata(t *testing.T) {
	origCommit := GitCommit
	origDate := BuildDate
	defer func() {
		GitCommit = origCommit
		BuildDate = origDate
	}()

	GitCommit = "abc1234"
	BuildDate = "2026-10-10T09:00:00Z"

	str := Full()
	if !strings.Contains(str, "commit: abc1234") {
		t.Fatalf("expected Full() to contain commit, got: %s", str)
	}
	if !strings.Contains(str, "built: 2026-10-10T09:00:00Z") {
		t.Fatalf("expected Full() to contain build date, got: %s", str)
	}
}
