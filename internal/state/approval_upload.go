package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type approvalUploadEnvelope struct {
	Version int                `json:"native_approval_upload_version"`
	Upload  core.UploadAttempt `json:"upload"`
}

func approvalUploadKey(id string) string { return "native-approval-upload:" + id }
func decodeApprovalUpload(raw []byte, id string) (core.UploadAttempt, error) {
	var envelope approvalUploadEnvelope
	if raw == nil {
		return core.UploadAttempt{}, core.ErrUploadUnknown
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != 1 || envelope.Upload.Request.ID != id {
		return core.UploadAttempt{}, fmt.Errorf("approval upload state identity or version mismatch")
	}
	return envelope.Upload, envelope.Upload.Validate()
}
func (s *Files) ReadApprovalUpload(ctx context.Context, id string) (core.UploadAttempt, error) {
	if err := ctx.Err(); err != nil {
		return core.UploadAttempt{}, err
	}
	if id == "" {
		return core.UploadAttempt{}, fmt.Errorf("approval upload identity is required")
	}
	hash := sha256.Sum256([]byte(approvalUploadKey(id)))
	raw, err := os.ReadFile(filepath.Join(s.root, hex.EncodeToString(hash[:])+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return core.UploadAttempt{}, core.ErrUploadUnknown
	}
	if err != nil {
		return core.UploadAttempt{}, err
	}
	return decodeApprovalUpload(raw, id)
}
func (s *Files) BeginApprovalUpload(ctx context.Context, request core.UploadRequest, retryRejected bool) (core.UploadAttempt, bool, error) {
	if err := request.Validate(); err != nil {
		return core.UploadAttempt{}, false, err
	}
	var attempt core.UploadAttempt
	created := false
	err := s.Transaction(ctx, approvalUploadKey(request.ID), func(raw json.RawMessage) (any, error) {
		var err error
		attempt, err = decodeApprovalUpload(raw, request.ID)
		if err != nil && !errors.Is(err, core.ErrUploadUnknown) {
			return nil, err
		}
		if err == nil {
			if attempt.Request != request {
				return nil, core.ErrConflict
			}
			if !retryRejected || attempt.Runs[len(attempt.Runs)-1].State != "rejected" {
				return nil, nil
			}
		} else {
			attempt.Request = request
		}
		now := time.Now().UTC()
		if len(attempt.Runs) > 0 && now.Before(attempt.Runs[len(attempt.Runs)-1].FinishedAt) {
			now = attempt.Runs[len(attempt.Runs)-1].FinishedAt
		}
		attempt.Runs = append(attempt.Runs, core.UploadRun{State: "uploading", StartedAt: now})
		if err := attempt.Validate(); err != nil {
			return nil, err
		}
		created = true
		return approvalUploadEnvelope{1, attempt}, nil
	})
	return attempt, created, err
}
func (s *Files) UpdateApprovalUpload(ctx context.Context, id string, update func(*core.UploadAttempt) error) (core.UploadAttempt, error) {
	var attempt core.UploadAttempt
	err := s.Transaction(ctx, approvalUploadKey(id), func(raw json.RawMessage) (any, error) {
		var err error
		attempt, err = decodeApprovalUpload(raw, id)
		if err != nil {
			return nil, err
		}
		request, count := attempt.Request, len(attempt.Runs)
		prefix, _ := json.Marshal(attempt.Runs[:count-1])
		old := attempt.Runs[count-1]
		oldJSON, _ := json.Marshal(old)
		var confirmed core.Identity
		if old.Reference != nil {
			confirmed = *old.Reference
		}
		if err := update(&attempt); err != nil {
			return nil, err
		}
		if attempt.Request != request || len(attempt.Runs) != count {
			return nil, core.ErrConflict
		}
		after, _ := json.Marshal(attempt.Runs[:count-1])
		last := attempt.Runs[count-1]
		lastJSON, _ := json.Marshal(last)
		if string(after) != string(prefix) || last.StartedAt != old.StartedAt {
			return nil, core.ErrConflict
		}
		if last.State == old.State && string(lastJSON) != string(oldJSON) {
			return nil, core.ErrConflict
		}
		allowed := last.State == old.State || old.State == "uploading" || (old.State == "unknown" && last.State == "uploaded")
		if !allowed || (!old.FinishedAt.IsZero() && last.FinishedAt.Before(old.FinishedAt)) {
			return nil, core.ErrConflict
		}
		if old.Reference != nil && (last.Reference == nil || *last.Reference != confirmed) {
			return nil, core.ErrConflict
		}
		if err := attempt.Validate(); err != nil {
			return nil, err
		}
		return approvalUploadEnvelope{1, attempt}, nil
	})
	return attempt, err
}
