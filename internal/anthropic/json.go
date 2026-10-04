//go:build !no_anthropic

package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
)

// JSONMessage accepts a complete JSON object, optionally in one Markdown fence.
// It rejects truncated generations and prose around JSON instead of guessing.
func JSONMessage(msg *sdk.Message) (json.RawMessage, error) {
	if msg == nil || msg.StopReason == "max_tokens" {
		return nil, fmt.Errorf("model response is incomplete")
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	s := strings.TrimSpace(text.String())
	if strings.HasPrefix(s, "```") {
		line, rest, ok := strings.Cut(s, "\n")
		if !ok || (line != "```json" && line != "```") || !strings.HasSuffix(rest, "```") {
			return nil, fmt.Errorf("model returned invalid JSON fence")
		}
		s = strings.TrimSpace(strings.TrimSuffix(rest, "```"))
	}
	if !strings.HasPrefix(s, "{") || !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("model returned no valid JSON object")
	}
	return json.RawMessage(s), nil
}
