package skill

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultDoubaoSkill(t *testing.T) {
	content := DefaultDoubaoSkill()
	if strings.TrimSpace(content) == "" {
		t.Fatal("expected non-empty embedded doubao skill content")
	}
	if !strings.Contains(content, "海外易商卡报销与小票机审助手") {
		t.Errorf("content does not contain expected title: %s", content[:100])
	}
}

func TestExportToFile(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportToFile("-", &buf); err != nil {
		t.Fatalf("export to stdout buffer failed: %v", err)
	}
	if !strings.Contains(buf.String(), "海外易商卡报销与小票机审助手") {
		t.Errorf("stdout buffer missing expected content")
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "exported_skill.md")
	if err := ExportToFile(outPath, nil); err != nil {
		t.Fatalf("export to file failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read exported file failed: %v", err)
	}
	if string(data) != buf.String() {
		t.Errorf("exported file content differs from stdout content")
	}
}
