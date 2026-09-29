//go:build no_anyreceipt

package main

import (
	"fmt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
)

func newAnyreceipt(string) (receipt.Recognizer, error) {
	return nil, fmt.Errorf("Anyreceipt was excluded at build time")
}
