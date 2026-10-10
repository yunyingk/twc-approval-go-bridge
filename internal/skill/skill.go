package skill

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"
)

//go:embed embedded/doubao_builtin_skill.md
var doubaoBuiltinSkillContent string

// DefaultDoubaoSkill returns the embedded markdown document for Doubao's builtin skill.
func DefaultDoubaoSkill() string {
	return doubaoBuiltinSkillContent
}

// ExportToFile exports the embedded Doubao skill document to the target file path.
// If targetPath is empty or "-", it writes to stdout.
func ExportToFile(targetPath string, output io.Writer) error {
	content := DefaultDoubaoSkill()
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("embedded doubao skill document is empty")
	}

	if targetPath == "" || targetPath == "-" {
		_, err := io.WriteString(output, content)
		return err
	}

	return os.WriteFile(targetPath, []byte(content), 0644)
}
