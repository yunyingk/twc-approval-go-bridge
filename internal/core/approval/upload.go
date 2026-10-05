package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

var (
	ErrUploadUnknown   = errors.New("unknown approval upload")
	ErrUploadReconcile = errors.New("approval upload requires external reconciliation")
	ErrUploadRejected  = errors.New("approval upload was explicitly rejected")
)

// UploadRequest pins the original content and both identities. Binary data and
// temporary download URLs never enter the durable upload record.
type UploadRequest struct {
	ID             string `json:"id"`
	SourceScope    string `json:"source_scope"`
	SourceIdentity string `json:"source_identity"`
	RecordID       string `json:"record_id"`
	FieldID        string `json:"field_id"`
	SourceFileID   string `json:"source_file_id"`
	TargetScope    string `json:"target_scope"`
	ContentHash    string `json:"content_hash"`
	ContentType    string `json:"content_type"`
	Size           int64  `json:"size"`
	Name           string `json:"name"`
	Kind           string `json:"kind"` // attachment or image; selected by form purpose, not inferred from MIME
}

func (r UploadRequest) identity() string {
	r.ID = ""
	raw, _ := json.Marshal(r)
	hash := sha256.Sum256(append([]byte("approval-upload-v1:"), raw...))
	return hex.EncodeToString(hash[:])
}
func NewUploadRequest(request UploadRequest) (UploadRequest, error) {
	request.ID = request.identity()
	return request, request.Validate()
}
func (r UploadRequest) Validate() error {
	for _, value := range []string{r.SourceScope, r.SourceIdentity, r.RecordID, r.FieldID, r.SourceFileID, r.TargetScope, r.ContentType} {
		if !validID(value) {
			return fmt.Errorf("approval upload requires explicit source, target and content metadata")
		}
	}
	hash, err := hex.DecodeString(r.ContentHash)
	if err != nil || len(hash) != sha256.Size || strings.ToLower(r.ContentHash) != r.ContentHash {
		return fmt.Errorf("approval upload requires an exact SHA-256 content hash")
	}
	if !validID(r.Name) || path.Base(r.Name) != r.Name || strings.ContainsAny(r.Name, "\\\r\n\x00") || path.Ext(r.Name) == "" || path.Ext(r.Name) == "." {
		return fmt.Errorf("approval upload requires a filename with extension")
	}
	limit := int64(50 << 20)
	if r.Kind == "image" {
		limit = 10 << 20
	} else if r.Kind != "attachment" {
		return fmt.Errorf("approval upload kind is unsupported")
	}
	if r.Size <= 0 || r.Size > limit || r.ID != r.identity() {
		return fmt.Errorf("approval upload size or immutable identity is invalid")
	}
	return nil
}

type UploadRun struct {
	State      string    `json:"state"` // uploading, unknown, rejected, uploaded
	Reference  *Identity `json:"reference,omitempty"`
	Failure    *Failure  `json:"failure,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
type UploadAttempt struct {
	Request UploadRequest `json:"request"`
	Runs    []UploadRun   `json:"runs"` // Only explicit retries of proven rejection append a run.
}

func (a UploadAttempt) Validate() error {
	if err := a.Request.Validate(); err != nil {
		return err
	}
	if len(a.Runs) == 0 {
		return fmt.Errorf("approval upload lacks a durable run")
	}
	var previous time.Time
	for n, run := range a.Runs {
		if run.StartedAt.IsZero() || run.StartedAt.Before(previous) {
			return fmt.Errorf("approval upload history timestamps are invalid")
		}
		switch run.State {
		case "uploading":
			if run.Reference != nil || run.Failure != nil || !run.FinishedAt.IsZero() {
				return ErrConflict
			}
		case "unknown", "rejected":
			code := "upload_unknown"
			if run.State == "rejected" {
				code = "upload_rejected"
			}
			if run.Reference != nil || run.Failure == nil || run.Failure.Phase != "upload" || run.Failure.Code != code || run.Failure.OccurredAt.IsZero() {
				return ErrConflict
			}
		case "uploaded":
			if run.Reference == nil || run.Reference.Scope != a.Request.TargetScope || !validID(run.Reference.ID) || run.Failure != nil {
				return ErrConflict
			}
		default:
			return fmt.Errorf("approval upload state is invalid")
		}
		if run.State != "uploading" && (run.FinishedAt.IsZero() || run.FinishedAt.Before(run.StartedAt)) {
			return ErrConflict
		}
		if n < len(a.Runs)-1 && run.State != "rejected" {
			return ErrConflict
		}
		previous = run.FinishedAt
	}
	return nil
}
