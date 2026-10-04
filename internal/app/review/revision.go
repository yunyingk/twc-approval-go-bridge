package review

import (
	"context"
	"errors"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

const (
	RevisionNotChecked     = "not_checked"
	RevisionCurrent        = "current"
	RevisionChanged        = "changed"
	RevisionSourceMismatch = "source_mismatch"
	RevisionSourceRemoved  = "source_removed"
	RevisionNoAttachments  = "no_attachments"
	RevisionUnavailable    = "unavailable"
)

// checkRevision pins the saved implementation, even after provider selection
// changes. It reads current evidence without calling a reviewer or writing state.
func checkRevision(ctx context.Context, source Source, request core.Request) (string, error) {
	if scoped, ok := source.(ScopedSource); ok && scoped.ReviewLogicalID(request.Document.RecordID) != request.LogicalID {
		return RevisionSourceMismatch, nil
	}
	// In-flight work keeps its original implementation and rules, even after
	// configuration changes. Only changed business evidence invalidates its result.
	pinned := Service{source: source, options: Options{Provider: request.Provider, ProviderVersion: request.ProviderVersion, RulesVersion: request.RulesVersion}}
	current, err := pinned.Prepare(ctx, request.Document.RecordID)
	switch {
	case errors.Is(err, core.ErrSourceRemoved):
		return RevisionSourceRemoved, nil
	case errors.Is(err, core.ErrNoAttachments):
		return RevisionNoAttachments, nil
	case err != nil:
		return RevisionUnavailable, err
	case current.Revision != request.Revision:
		return RevisionChanged, nil
	default:
		return RevisionCurrent, nil
	}
}
