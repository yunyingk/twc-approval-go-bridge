//go:build !no_anthropic

package review

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// LoadRules is called only when constructing the self-hosted reviewer.
// The bridge's main configuration and Seal adapter never read this file.
func LoadRules(path string) (Rules, error) {
	file, err := os.Open(path)
	if err != nil {
		return Rules{}, fmt.Errorf("read self-hosted review rules: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return Rules{}, fmt.Errorf("self-hosted rules must be a regular file of at most 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var rules Rules
	if err := decoder.Decode(&rules); err != nil {
		return Rules{}, fmt.Errorf("invalid self-hosted rules JSON or unknown field")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Rules{}, fmt.Errorf("self-hosted rules require exactly one JSON object")
	}
	if strings.TrimSpace(rules.Version) == "" || strings.TrimSpace(rules.Instructions) == "" {
		return Rules{}, fmt.Errorf("self-hosted rules require version and instructions")
	}
	return rules, nil
}
