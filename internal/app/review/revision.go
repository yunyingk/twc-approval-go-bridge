package review

import (
	"context"
	"errors"
	"fmt"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// PreparePinned re-reads current facts with a saved implementation/rules pin.
// It has no reviewer, writer or store and never submits or changes state.
func PreparePinned(ctx context.Context, source Source, request core.Request) (core.Request, error) {
	if source == nil || request.Provider == "" || request.Document.RecordID == "" {
		return core.Request{}, fmt.Errorf("pinned preparation requires source and saved request identity")
	}
	if scoped, ok := source.(ScopedSource); ok && scoped.ReviewLogicalID(request.Document.RecordID) != request.LogicalID {
		return core.Request{}, fmt.Errorf("pinned preparation source mismatch")
	}
	pinned := Service{source: source, options: Options{Provider: request.Provider, ProviderVersion: request.ProviderVersion, RulesVersion: request.RulesVersion, Versioned: true}}
	return pinned.Prepare(ctx, request.Document.RecordID)
}

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
	current, err := PreparePinned(ctx, source, request)
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
