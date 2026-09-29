//go:build no_anthropic

package main

import (
	"fmt"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

func newModel(string, string, string) (invoice.Recognizer, error) {
	return nil, fmt.Errorf("Anthropic model was excluded at build time")
}
