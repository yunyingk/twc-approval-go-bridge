package main

import (
	"log/slog"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func reviewChangesEnabled(cfg config.Config) bool {
	return cfg.Business != nil && (cfg.Business.Review.ResubmitOnDetailChange || cfg.Business.Review.ResubmitOnSourceChange)
}

// Watch the fields actually used by ReviewSource, not workflow or derived
// columns. Native link edits are handled by the separate detail-change option.
func reviewSourceBindings(profile *config.BusinessProfile) []events.ReviewSourceBinding {
	fields := func(mapping map[string]string, semantics ...string) []string {
		var ids []string
		for _, semantic := range semantics {
			if id := mapping[semantic]; id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	ledger := profile.Tables.InvoiceLedger
	bindings := []events.ReviewSourceBinding{{Kind: app.InvoiceSource, BaseToken: ledger.BaseToken, TableID: ledger.TableID, InvoiceNumberFieldID: ledger.Fields["invoice_number"],
		FieldIDs: fields(ledger.Fields, "bridge_attachment_key", "attachment_key", "source_key", "raw_json", "title", "invoice_number", "receipt_type", "business_category", "seller", "buyer", "currency", "pretax_amount", "tax_amount", "tax_rate", "total_amount", "country", "issue_date")}}
	if profile.Review.IncludeTransactions {
		table := profile.Tables.Transactions
		bindings = append(bindings, events.ReviewSourceBinding{Kind: app.TransactionSource, BaseToken: table.BaseToken, TableID: table.TableID,
			FieldIDs: fields(table.Fields, "transaction_id", "merchant", "transaction_time", "original_amount", "original_currency", "booked_amount_cny", "country", "transaction_type", "transaction_status")})
	}
	return bindings
}

func newReviewSourceChanges(cfg config.Config, automatic *app.Automatic, logger *slog.Logger) (*app.SourceChanges, *events.ReviewSourceChangeSink, error) {
	details := cfg.Business.Tables.ReimbursementDetails
	scope := "feishu:" + details.BaseToken + ":" + details.TableID
	bindings := reviewSourceBindings(cfg.Business)
	sources := make(map[app.SourceKind]string, len(bindings))
	for _, binding := range bindings {
		sources[binding.Kind] = "feishu:" + binding.BaseToken + ":" + binding.TableID
	}
	store, err := state.NewFiles(cfg.Runtime.StateDir)
	if err != nil {
		return nil, nil, err
	}
	flow, err := app.NewSourceChanges(scope, sources, store, automatic, logger)
	if err != nil {
		return nil, nil, err
	}
	sink, err := events.NewReviewSourceChangeSink(scope, bindings, flow)
	if err != nil {
		return nil, nil, err
	}
	return flow, sink, nil
}
