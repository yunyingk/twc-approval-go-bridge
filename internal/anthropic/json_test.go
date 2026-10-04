//go:build !no_anthropic

package anthropic

import (
	sdk "github.com/anthropics/anthropic-sdk-go"
	"testing"
)

func TestJSONMessageAcceptsFenceButRejectsTruncationAndProse(t *testing.T) {
	for _, item := range []struct {
		text, stop string
		valid      bool
	}{{"```json\n{\"facts\":{}}\n```", "end_turn", true}, {"{\"facts\":{}}", "max_tokens", false}, {"Here is JSON: {\"facts\":{}}", "end_turn", false}} {
		message := &sdk.Message{StopReason: sdk.StopReason(item.stop), Content: []sdk.ContentBlockUnion{{Type: "text", Text: item.text}}}
		_, err := JSONMessage(message)
		if (err == nil) != item.valid {
			t.Fatalf("unexpected parsing result for %q: %v", item.text, err)
		}
	}
}
