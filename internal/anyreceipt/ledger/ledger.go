// Package ledger maps receipt recognition results to the invoice ledger.
// Deprecated: the Feishu mapping now lives in feishu/base/invoiceledger.
package ledger

import "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/invoiceledger"

type Config = invoiceledger.Config
type Store = invoiceledger.Store
type Handler = invoiceledger.Handler

var New = invoiceledger.New

const (
	SourceKey        = invoiceledger.SourceKey
	RawJSON          = invoiceledger.RawJSON
	DetailID         = invoiceledger.DetailID
	Relation         = invoiceledger.Relation
	UniqueKey        = invoiceledger.UniqueKey
	Title            = invoiceledger.Title
	Number           = invoiceledger.Number
	ReceiptType      = invoiceledger.ReceiptType
	BusinessCategory = invoiceledger.BusinessCategory
	Seller           = invoiceledger.Seller
	Buyer            = invoiceledger.Buyer
	Currency         = invoiceledger.Currency
	Pretax           = invoiceledger.Pretax
	Tax              = invoiceledger.Tax
	TaxRate          = invoiceledger.TaxRate
	Total            = invoiceledger.Total
	IssueDate        = invoiceledger.IssueDate
	Country          = invoiceledger.Country
	AISummary        = invoiceledger.AISummary
)
