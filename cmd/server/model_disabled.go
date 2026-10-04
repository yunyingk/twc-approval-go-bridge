//go:build no_anthropic

package main

import (
	"fmt"

	appreview "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

func newModel(string, string, string) (invoice.Recognizer, error) {
	return nil, fmt.Errorf("Anthropic model was excluded at build time")
}

func newModelReview(config.Config) (appreview.Reviewer, string, string, error) {
	return nil, "", "", fmt.Errorf("Anthropic review was excluded at build time")
}
