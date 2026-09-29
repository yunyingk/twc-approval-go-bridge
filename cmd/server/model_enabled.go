//go:build !no_anthropic

package main

import (
	"github.com/yunyingk/twc-approval-go-bridge/internal/anthropic/model"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

func newModel(apiKey, baseURL, name string) (invoice.Recognizer, error) {
	return model.New(apiKey, baseURL, name)
}
