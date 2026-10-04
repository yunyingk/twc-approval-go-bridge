// Package receiptcompat reads historical OCR payloads at adapter boundaries.
// New business code consumes invoice.Facts, never provider field spellings.
package receiptcompat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

func Decode(raw json.RawMessage) (invoice.Recognition, error) {
	var payload struct {
		MultipleReceipts bool                       `json:"multiple_receipts"`
		Facts            *invoice.Facts             `json:"facts"`
		Origin           invoice.Origin             `json:"origin"`
		Outputs          map[string]json.RawMessage `json:"outputs"`
		Data             struct {
			Outputs map[string]json.RawMessage `json:"outputs"`
		} `json:"data"`
		Summary string `json:"summary"`
		TraceID string `json:"traceId"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return invoice.Recognition{}, fmt.Errorf("decode receipt JSON")
	}
	if payload.MultipleReceipts {
		return invoice.Recognition{}, fmt.Errorf("multiple receipts in one file are not supported yet")
	}
	if payload.Data.Outputs != nil {
		payload.Outputs = payload.Data.Outputs
	}
	if payload.Facts == nil && payload.Outputs == nil {
		return invoice.Recognition{}, fmt.Errorf("receipt JSON has no facts or outputs")
	}
	r := invoice.Recognition{Facts: payload.Facts, Origin: payload.Origin, Outputs: payload.Outputs, Summary: payload.Summary, Raw: raw}
	if r.Origin.TraceID == "" {
		r.Origin.TraceID = payload.TraceID
	}
	return Normalize(r), nil
}

// Normalize is also used for legacy in-process recognizers during migration.
func Normalize(r invoice.Recognition) invoice.Recognition {
	if r.Facts != nil {
		return r
	}
	value := func(key, fallback string) string {
		raw := r.Outputs[key]
		if len(raw) == 0 {
			return strings.TrimSpace(fallback)
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return strings.TrimSpace(s)
		}
		var n json.Number
		if string(raw) != "null" && json.Unmarshal(raw, &n) == nil {
			return n.String()
		}
		return strings.TrimSpace(fallback)
	}
	r.Facts = &invoice.Facts{
		Title: value("title", r.Title), Country: value("country", r.Country), ReceiptType: value("type", r.Type),
		Number: value("Number", r.Number), IssueDate: value("DateofInssuance", r.Date), Currency: strings.ToUpper(value("currency", "")),
		Seller: value("Seller", ""), Buyer: value("Buyer", ""), BusinessCategory: value("TypeofBill", ""),
		TaxRate: value("taxrate", ""), Total: value("total", r.Total), Tax: value("tax", r.Tax), Pretax: value("amountwithouttax", ""),
	}
	return r
}
