package approval_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func uploadPayload(t *testing.T, id string) app.UploadPayload {
	t.Helper()
	data := []byte("PRIVATE_BINARY_CONTENT:" + id)
	hash := sha256.Sum256(data)
	request, err := core.NewUploadRequest(core.UploadRequest{SourceScope: "source", SourceIdentity: "source-app", RecordID: "record-" + id, FieldID: "attachment", SourceFileID: "source-file-" + id, TargetScope: "target", ContentHash: hex.EncodeToString(hash[:]), ContentType: "text/plain", Size: int64(len(data)), Name: "invoice.txt", Kind: "attachment"})
	if err != nil {
		t.Fatal(err)
	}
	return app.UploadPayload{Request: request, Data: data, Semantic: "files"}
}

type countedUploader struct {
	calls    atomic.Int64
	err      error
	callback func()
}

func (*countedUploader) TargetScope() string { return "target" }
func (u *countedUploader) Upload(context.Context, core.UploadRequest, []byte) (core.Identity, error) {
	u.calls.Add(1)
	if u.callback != nil {
		u.callback()
	}
	return core.Identity{Scope: "target", ID: "approval-file-code"}, u.err
}

func TestApprovalUploadsReserveOnceAcrossStoreHandlesAndReuseAfterRestart(t *testing.T) {
	root := t.TempDir()
	uploader := &countedUploader{}
	payload := uploadPayload(t, "one")
	var workers sync.WaitGroup
	for n := 0; n < 12; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			store, err := state.OpenFiles(root)
			if err != nil {
				t.Error(err)
				return
			}
			manager, err := app.NewUploadManager(uploader, store)
			if err != nil {
				t.Error(err)
				return
			}
			_, err = manager.Upload(context.Background(), payload, false)
			if err != nil && !errors.Is(err, core.ErrUploadReconcile) {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	store, _ := state.OpenFiles(root)
	manager, _ := app.NewUploadManager(uploader, store)
	attempt, err := manager.Upload(context.Background(), payload, true)
	if err != nil || uploader.calls.Load() != 1 || len(attempt.Runs) != 1 || attempt.Runs[0].State != "uploaded" {
		t.Fatal("concurrent or restarted caller re-uploaded a saved file")
	}
	raw, _ := json.Marshal(attempt)
	if strings.Contains(string(raw), "PRIVATE_BINARY_CONTENT") {
		t.Fatal("original binary entered durable metadata")
	}
	for _, entry := range mustReadDirectory(t, root) {
		info, _ := entry.Info()
		if info.Mode().Perm() != 0600 {
			t.Fatal("upload metadata/lock permissions are not private")
		}
	}
	if pending, err := store.PendingReviews(context.Background()); err != nil || len(pending) != 0 {
		t.Fatal("upload namespace entered AI delivery scanner")
	}
}
func mustReadDirectory(t *testing.T, root string) []os.DirEntry {
	t.Helper()
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestApprovalUploadLostResponseStaysUnknownAndCannotBeExplicitlyResent(t *testing.T) {
	store, _ := state.NewFiles(t.TempDir())
	uploader := &countedUploader{err: errors.New("PRIVATE_LOST_RESPONSE")}
	manager, _ := app.NewUploadManager(uploader, store)
	payload := uploadPayload(t, "unknown")
	attempt, err := manager.Upload(context.Background(), payload, false)
	if err == nil || strings.Contains(err.Error(), "PRIVATE") || attempt.Runs[0].State != "unknown" {
		t.Fatal("lost upload response was accepted or exposed")
	}
	raw, _ := json.Marshal(attempt)
	if strings.Contains(string(raw), "PRIVATE_LOST_RESPONSE") {
		t.Fatal("raw upstream error saved")
	}
	_, err = manager.Upload(context.Background(), payload, true)
	if !errors.Is(err, core.ErrUploadReconcile) || uploader.calls.Load() != 1 {
		t.Fatal("explicit rejected retry reset an uncertain upload")
	}
	if _, err := store.UpdateApprovalUpload(context.Background(), payload.Request.ID, func(a *core.UploadAttempt) error {
		a.Runs[0].State = "rejected"
		a.Runs[0].Failure.Code = "upload_rejected"
		return nil
	}); !errors.Is(err, core.ErrConflict) {
		t.Fatal("unknown upload downgraded into safe rejection")
	}
}

func TestApprovalRejectedUploadNeedsExplicitRetryAndRetainsHistory(t *testing.T) {
	store, _ := state.NewFiles(t.TempDir())
	uploader := &countedUploader{err: core.ErrUploadRejected}
	manager, _ := app.NewUploadManager(uploader, store)
	payload := uploadPayload(t, "rejected")
	if _, err := manager.Upload(context.Background(), payload, false); !errors.Is(err, core.ErrUploadRejected) {
		t.Fatal("rejection not preserved")
	}
	uploader.err = nil
	if _, err := manager.Upload(context.Background(), payload, false); !errors.Is(err, core.ErrUploadRejected) || uploader.calls.Load() != 1 {
		t.Fatal("ordinary retry sent a rejected file again")
	}
	attempt, err := manager.Upload(context.Background(), payload, true)
	if err != nil || len(attempt.Runs) != 2 || attempt.Runs[0].State != "rejected" || attempt.Runs[1].State != "uploaded" || uploader.calls.Load() != 2 {
		t.Fatal("explicit retry lost rejection history or receipt")
	}
	for _, scenario := range []string{"old history", "request", "confirmed code", "in-place code", "finished time", "reset"} {
		_, err := store.UpdateApprovalUpload(context.Background(), payload.Request.ID, func(a *core.UploadAttempt) error {
			switch scenario {
			case "old history":
				a.Runs[0].Failure.HTTPStatus = 400
			case "request":
				a.Request.Name = "different.txt"
			case "confirmed code":
				ref := *a.Runs[1].Reference
				ref.ID = "another-code"
				a.Runs[1].Reference = &ref
			case "in-place code":
				a.Runs[1].Reference.ID = "another-code"
			case "finished time":
				a.Runs[1].FinishedAt = a.Runs[1].FinishedAt.Add(1)
			case "reset":
				a.Runs[1].State = "uploading"
				a.Runs[1].Reference = nil
			}
			return nil
		})
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("mutable upload history accepted: %s", scenario)
		}
	}
}

type uploadFaultStore struct {
	*state.Files
	beginAfterError bool
	resultError     bool
}

func (s *uploadFaultStore) BeginApprovalUpload(ctx context.Context, request core.UploadRequest, retry bool) (core.UploadAttempt, bool, error) {
	attempt, created, err := s.Files.BeginApprovalUpload(ctx, request, retry)
	if err == nil && s.beginAfterError {
		s.beginAfterError = false
		return attempt, created, errors.New("PRIVATE_SYNC_FAILURE")
	}
	return attempt, created, err
}
func (s *uploadFaultStore) UpdateApprovalUpload(ctx context.Context, id string, update func(*core.UploadAttempt) error) (core.UploadAttempt, error) {
	if s.resultError {
		s.resultError = false
		return core.UploadAttempt{}, errors.New("PRIVATE_RESULT_SAVE_FAILURE")
	}
	return s.Files.UpdateApprovalUpload(ctx, id, update)
}
func TestApprovalUploadPersistsNoCallProofAndHoldsAcceptedResultSaveFailure(t *testing.T) {
	for _, stage := range []string{"intent", "result"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			files, _ := state.NewFiles(root)
			store := &uploadFaultStore{Files: files, beginAfterError: stage == "intent", resultError: stage == "result"}
			uploader := &countedUploader{}
			manager, _ := app.NewUploadManager(uploader, store)
			payload := uploadPayload(t, stage)
			if _, err := manager.Upload(context.Background(), payload, false); err == nil {
				t.Fatal("persistence failure reported success")
			}
			reopened, err := state.OpenFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			manager, _ = app.NewUploadManager(uploader, reopened)
			attempt, err := reopened.ReadApprovalUpload(context.Background(), payload.Request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "intent" {
				if uploader.calls.Load() != 0 || attempt.Runs[0].State != "rejected" {
					t.Fatal("pre-call persistence failure lost no-call proof")
				}
				if _, err := manager.Upload(context.Background(), payload, true); err != nil || uploader.calls.Load() != 1 {
					t.Fatal("proven pre-call rejection could not be explicitly retried")
				}
			} else {
				if uploader.calls.Load() != 1 || attempt.Runs[0].State != "uploading" {
					t.Fatal("accepted result save failure discarded original attempt")
				}
				if _, err := manager.Upload(context.Background(), payload, true); !errors.Is(err, core.ErrUploadReconcile) || uploader.calls.Load() != 1 {
					t.Fatal("save failure triggered another POST")
				}
			}
		})
	}
}

func TestApprovalUploadSavesAcceptedReceiptAfterCancellationAndRejectsTamperedBytes(t *testing.T) {
	root := t.TempDir()
	store, _ := state.OpenFiles(root)
	payload := uploadPayload(t, "cancel")
	if _, err := store.ReadApprovalUpload(context.Background(), payload.Request.ID); !errors.Is(err, core.ErrUploadUnknown) || len(mustReadDirectory(t, root)) != 0 {
		t.Fatal("read-only missing receipt created state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	uploader := &countedUploader{callback: cancel}
	manager, _ := app.NewUploadManager(uploader, store)
	attempt, err := manager.Upload(ctx, payload, false)
	if err != nil || attempt.Runs[0].State != "uploaded" {
		t.Fatal("accepted file receipt disappeared on canceled context")
	}
	payload.Data = []byte("different bytes")
	if _, err := manager.Upload(context.Background(), payload, false); err == nil || uploader.calls.Load() != 1 {
		t.Fatal("tampered content reached uploader or cached file")
	}
}

func TestApprovalUploadCorruptExistingReceiptCannotBecomeAMissingUpload(t *testing.T) {
	for _, raw := range []string{"", "{}", "truncated"} {
		t.Run(map[string]string{"": "empty", "{}": "wrong version", "truncated": "malformed"}[raw], func(t *testing.T) {
			root := t.TempDir()
			store, _ := state.NewFiles(root)
			uploader := &countedUploader{}
			manager, _ := app.NewUploadManager(uploader, store)
			payload := uploadPayload(t, "corrupt")
			if _, err := manager.Upload(context.Background(), payload, false); err != nil {
				t.Fatal(err)
			}
			for _, entry := range mustReadDirectory(t, root) {
				if strings.HasSuffix(entry.Name(), ".json") {
					if err := os.WriteFile(filepath.Join(root, entry.Name()), []byte(raw), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := store.ReadApprovalUpload(context.Background(), payload.Request.ID); err == nil || errors.Is(err, core.ErrUploadUnknown) {
				t.Fatal("corrupt existing receipt was treated as an absent upload")
			}
			if _, err := manager.Upload(context.Background(), payload, true); err == nil || uploader.calls.Load() != 1 {
				t.Fatal("corrupt receipt triggered another file POST")
			}
		})
	}
}
