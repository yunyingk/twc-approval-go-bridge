package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type UploadReader interface {
	ReadApprovalUpload(context.Context, string) (core.UploadAttempt, error)
}
type UploadStore interface {
	UploadReader
	BeginApprovalUpload(context.Context, core.UploadRequest, bool) (core.UploadAttempt, bool, error)
	UpdateApprovalUpload(context.Context, string, func(*core.UploadAttempt) error) (core.UploadAttempt, error)
}
type Uploader interface {
	TargetScope() string
	Upload(context.Context, core.UploadRequest, []byte) (core.Identity, error)
}
type UploadPayload struct {
	Request  core.UploadRequest `json:"-"`
	Data     []byte             `json:"-"`
	Semantic string             `json:"-"`
}
type UploadManager struct {
	gateway Uploader
	store   UploadStore
}

func NewUploadManager(gateway Uploader, store UploadStore) (*UploadManager, error) {
	if gateway == nil || gateway.TargetScope() == "" || store == nil {
		return nil, fmt.Errorf("approval upload requires explicit target and persistent store")
	}
	return &UploadManager{gateway, store}, nil
}
func (m *UploadManager) Upload(ctx context.Context, payload UploadPayload, retryRejected bool) (core.UploadAttempt, error) {
	request := payload.Request
	if err := request.Validate(); err != nil {
		return core.UploadAttempt{}, err
	}
	hash := sha256.Sum256(payload.Data)
	if request.TargetScope != m.gateway.TargetScope() || int64(len(payload.Data)) != request.Size || hex.EncodeToString(hash[:]) != request.ContentHash {
		return core.UploadAttempt{}, fmt.Errorf("approval upload payload does not match its pinned content and target")
	}
	attempt, created, err := m.store.BeginApprovalUpload(ctx, request, retryRejected)
	if err != nil {
		if created && attempt.Request == request && len(attempt.Runs) > 0 {
			generation := len(attempt.Runs)
			// Begin may fail after atomic replacement (for example directory sync).
			// No file POST has happened: persist that proof if the store recovered.
			if saved, saveErr := m.store.UpdateApprovalUpload(context.WithoutCancel(ctx), request.ID, func(a *core.UploadAttempt) error {
				if a.Request != request || len(a.Runs) != generation || a.Runs[generation-1].State != "uploading" {
					return core.ErrConflict
				}
				last := &a.Runs[generation-1]
				last.State = "rejected"
				last.Failure = failure("upload", "upload_rejected", err)
				last.FinishedAt = time.Now().UTC()
				if last.FinishedAt.Before(last.StartedAt) {
					last.FinishedAt = last.StartedAt
				}
				return nil
			}); saveErr == nil {
				return saved, &uploadCallFailure{errors.Join(core.ErrUploadRejected, err), true}
			}
		}
		return attempt, err
	}
	if !created {
		last := attempt.Runs[len(attempt.Runs)-1]
		if last.State == "uploaded" {
			return attempt, nil
		}
		if last.State == "rejected" {
			return attempt, core.ErrUploadRejected
		}
		return attempt, core.ErrUploadReconcile
	}
	generation := len(attempt.Runs)
	ref, callErr := m.gateway.Upload(ctx, request, payload.Data)
	if callErr == nil && (ref.Scope != request.TargetScope || ref.ID == "" || ref.ID != strings.TrimSpace(ref.ID)) {
		callErr = core.ErrConflict
	}
	saved, saveErr := m.store.UpdateApprovalUpload(context.WithoutCancel(ctx), request.ID, func(a *core.UploadAttempt) error {
		if a.Request != request || len(a.Runs) != generation {
			return core.ErrConflict
		}
		last := &a.Runs[generation-1]
		if last.State == "uploaded" {
			if callErr == nil && *last.Reference != ref {
				return core.ErrConflict
			}
			return nil
		}
		if last.State != "uploading" {
			return core.ErrConflict
		}
		last.FinishedAt = time.Now().UTC()
		if last.FinishedAt.Before(last.StartedAt) {
			last.FinishedAt = last.StartedAt
		}
		if callErr != nil {
			last.State = "unknown"
			code := "upload_unknown"
			if errors.Is(callErr, core.ErrUploadRejected) {
				last.State = "rejected"
				code = "upload_rejected"
			}
			last.Failure = failure("upload", code, callErr)
		} else {
			last.State = "uploaded"
			last.Reference = &ref
		}
		return nil
	})
	if saveErr != nil {
		return attempt, fmt.Errorf("persist approval upload result: %w", saveErr)
	}
	if callErr != nil {
		return saved, &uploadCallFailure{callErr, errors.Is(callErr, core.ErrUploadRejected)}
	}
	return saved, nil
}

type uploadCallFailure struct {
	cause    error
	rejected bool
}

func (e *uploadCallFailure) Error() string {
	if e.rejected {
		return "approval upload was rejected"
	}
	return "approval upload result is unknown"
}
func (e *uploadCallFailure) Unwrap() error { return e.cause }
