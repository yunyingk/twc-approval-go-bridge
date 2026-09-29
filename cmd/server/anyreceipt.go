package main

import (
	"github.com/yunyingk/twc-approval-go-bridge/internal/anyreceipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

func newAnyreceipt(apiKey string) (invoice.Recognizer, error) {
	return anyreceipt.New(apiKey, nil)
}
