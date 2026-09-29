//go:build !no_anyreceipt

package main

import (
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/anyreceipt"
)

func newAnyreceipt(apiKey string) (receipt.Recognizer, error) {
	return anyreceipt.New(apiKey, nil)
}
