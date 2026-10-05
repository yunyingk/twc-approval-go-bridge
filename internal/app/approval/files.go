package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

// SourceFile is a private locator, not an approval file code or download URL.
type SourceFile struct {
	SourceScope, SourceIdentity, RecordID, FieldID, ID, Name string
	Size                                                     int64
}

func fileOnlyIssues(input InputCheck) bool {
	for _, issue := range input.Issues {
		if issue.Code != "approval_upload_required" {
			return false
		}
	}
	return true
}

// UploadDraft keeps the full selection. Rejected uploads require an explicit
// retry; uncertain uploads cannot be re-sent, even with that retry option.
func (i SourceInspection) UploadDraft(retryRejected bool) ([]core.Row, []UploadPayload, error) {
	if len(i.drafts) == 0 || len(i.drafts) != len(i.Inputs) {
		return nil, nil, ErrInputsNotReady
	}
	prepared := map[string]bool{}
	for _, payload := range i.payloads {
		prepared[payload.Request.RecordID+"\x00"+payload.Semantic] = true
	}
	for _, input := range i.Inputs {
		for _, issue := range input.Issues {
			if (issue.Code == "approval_upload_required" || issue.Code == "approval_upload_rejected") && !prepared[input.RecordID+"\x00"+issue.Input] {
				return nil, nil, ErrInputsNotReady
			}
			switch issue.Code {
			case "approval_upload_required":
			case "approval_upload_rejected":
				if !retryRejected {
					return nil, nil, core.ErrUploadRejected
				}
			case "approval_upload_reconcile":
				return nil, nil, core.ErrUploadReconcile
			default:
				return nil, nil, ErrInputsNotReady
			}
		}
	}
	return i.drafts, i.payloads, nil
}

func reviewedFile(input SourceFile, semantic, target string, row core.Row, evidence *ReviewEvidence) (UploadPayload, string) {
	if input.SourceScope != row.SourceScope || input.RecordID != row.RecordID {
		return UploadPayload{}, "file_not_reviewed"
	}
	if input.Size < 0 {
		return UploadPayload{}, "file_metadata_invalid"
	}
	for _, invoice := range evidence.Current.Document.Invoices {
		if invoice.FileToken != input.ID {
			continue
		}
		file := invoice.Attachment
		if len(file.Data) == 0 {
			return UploadPayload{}, "file_content_missing"
		}
		if (input.Name != "" && input.Name != file.Name) || (input.Size > 0 && input.Size != int64(len(file.Data))) {
			return UploadPayload{}, "file_metadata_changed"
		}
		hash := sha256.Sum256(file.Data)
		request, err := core.NewUploadRequest(core.UploadRequest{SourceScope: input.SourceScope, SourceIdentity: input.SourceIdentity, RecordID: input.RecordID, FieldID: input.FieldID, SourceFileID: input.ID, TargetScope: target,
			ContentHash: hex.EncodeToString(hash[:]), ContentType: file.ContentType, Size: int64(len(file.Data)), Name: file.Name, Kind: "attachment"})
		if err != nil {
			return UploadPayload{}, "file_metadata_invalid"
		}
		return UploadPayload{Request: request, Data: file.Data, Semantic: semantic}, ""
	}
	return UploadPayload{}, "file_not_reviewed"
}

// resolveFiles reads only saved receipts, pinned to the bytes already checked
// by ReviewGate. It has no uploader and cannot make a missing receipt appear.
func (s *PreparedSource) resolveFiles(ctx context.Context, input *InputCheck, row *core.Row, evidence *ReviewEvidence) []UploadPayload {
	if len(input.Files) == 0 {
		return nil
	}
	if s.uploads == nil {
		for semantic := range input.Files {
			input.Issues = append(input.Issues, InputIssue{semantic, "approval_upload_state_unavailable"})
		}
		return nil
	}
	issues := []InputIssue{}
	for _, issue := range input.Issues {
		if issue.Code != "approval_upload_required" || len(input.Files[issue.Input]) == 0 {
			issues = append(issues, issue)
		}
	}
	semantics := make([]string, 0, len(input.Files))
	for semantic := range input.Files {
		semantics = append(semantics, semantic)
	}
	sort.Strings(semantics)
	payloads := []UploadPayload{}
	for _, semantic := range semantics {
		refs := []core.Identity{}
		artifacts := []string{}
		complete := true
		for _, file := range input.Files[semantic] {
			payload, code := reviewedFile(file, semantic, s.targetScope, *row, evidence)
			if code != "" {
				issues = append(issues, InputIssue{semantic, code})
				complete = false
				continue
			}
			payloads = append(payloads, payload)
			attempt, err := s.uploads.ReadApprovalUpload(ctx, payload.Request.ID)
			if errors.Is(err, core.ErrUploadUnknown) {
				code = "approval_upload_required"
			} else if err != nil {
				code = "approval_upload_state_unavailable"
			} else if attempt.Validate() != nil || attempt.Request != payload.Request {
				code = "approval_upload_state_invalid"
			} else {
				last := attempt.Runs[len(attempt.Runs)-1]
				switch last.State {
				case "uploaded":
					refs = append(refs, *last.Reference)
					artifacts = append(artifacts, payload.Request.ID)
				case "rejected":
					code = "approval_upload_rejected"
				default:
					code = "approval_upload_reconcile"
				}
			}
			if code != "" {
				issues = append(issues, InputIssue{semantic, code})
				complete = false
			}
		}
		if complete && len(refs) > 0 {
			row.Fields[semantic] = core.Value{Kind: "files", References: refs, Artifacts: artifacts}
		}
	}
	input.Issues = issues
	return payloads
}
