package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// OpenFiles opens existing state without creating a directory or a lock file.
func OpenFiles(root string) (*Files, error) {
	if root == "" {
		return nil, fmt.Errorf("state directory is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("state path is not a directory")
	}
	return &Files{root: root}, nil
}

func (s *Files) ReadReview(ctx context.Context, documentID string) (core.Attempt, error) {
	if err := ctx.Err(); err != nil {
		return core.Attempt{}, err
	}
	if documentID == "" {
		return core.Attempt{}, fmt.Errorf("review document ID is required")
	}
	hash := sha256.Sum256([]byte("review:" + documentID))
	raw, err := os.ReadFile(filepath.Join(s.root, hex.EncodeToString(hash[:])+".json"))
	if err != nil {
		return core.Attempt{}, err
	}
	var attempt core.Attempt
	if err := json.Unmarshal(raw, &attempt); err != nil {
		return core.Attempt{}, err
	}
	if attempt.Request.Document.DocumentID != documentID {
		return core.Attempt{}, fmt.Errorf("review state identity mismatch")
	}
	return attempt, nil
}
