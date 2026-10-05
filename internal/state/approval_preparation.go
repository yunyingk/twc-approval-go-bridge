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

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type approvalPreparationEnvelope struct {
	Version int                `json:"native_approval_preparation_version"`
	Batch   core.PreparedBatch `json:"batch"`
}

func approvalPreparationKey(id string) string { return "native-approval-preparation:" + id }
func decodeApprovalPreparation(raw []byte, id string) (core.PreparedBatch, error) {
	if raw == nil {
		return core.PreparedBatch{}, core.ErrPreparationUnknown
	}
	var envelope approvalPreparationEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != 1 || envelope.Batch.ID != id {
		return core.PreparedBatch{}, fmt.Errorf("approval preparation identity or version mismatch")
	}
	return envelope.Batch, envelope.Batch.Validate()
}
func (s *Files) ApprovalPreparationPath(id string) (string, error) {
	hash, err := hex.DecodeString(id)
	if err != nil || len(hash) != sha256.Size || id != hex.EncodeToString(hash) {
		return "", fmt.Errorf("approval preparation requires a valid digest")
	}
	key := sha256.Sum256([]byte(approvalPreparationKey(id)))
	return filepath.Abs(filepath.Join(s.root, hex.EncodeToString(key[:])+".json"))
}
func (s *Files) ReadApprovalPreparation(ctx context.Context, id string) (core.PreparedBatch, error) {
	if err := ctx.Err(); err != nil {
		return core.PreparedBatch{}, err
	}
	path, err := s.ApprovalPreparationPath(id)
	if err != nil {
		return core.PreparedBatch{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return core.PreparedBatch{}, core.ErrPreparationUnknown
	}
	if err != nil {
		return core.PreparedBatch{}, err
	}
	return decodeApprovalPreparation(raw, id)
}

// SaveApprovalPreparation atomically saves the whole selection without creating
// an instance attempt or claiming a member. A repeated audit keeps its original
// observation time and never overwrites an existing damaged/different record.
func (s *Files) SaveApprovalPreparation(ctx context.Context, batch core.PreparedBatch) (core.PreparedBatch, error) {
	if err := batch.Validate(); err != nil {
		return core.PreparedBatch{}, err
	}
	var saved core.PreparedBatch
	err := s.Transaction(ctx, approvalPreparationKey(batch.ID), func(raw json.RawMessage) (any, error) {
		old, err := decodeApprovalPreparation(raw, batch.ID)
		if err == nil {
			saved = old
			return nil, nil
		}
		if !errors.Is(err, core.ErrPreparationUnknown) {
			return nil, err
		}
		saved = batch
		return approvalPreparationEnvelope{1, batch}, nil
	})
	return saved, err
}
