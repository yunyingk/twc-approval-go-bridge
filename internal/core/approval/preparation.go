package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

var ErrPreparationUnknown = errors.New("unknown approval preparation")

// RequestArtifact is opaque adapter output. Core verifies its bytes, but never
// interprets platform controls, SDK fields, authorization or endpoint semantics.
type RequestArtifact struct {
	Format string          `json:"format"`
	Hash   string          `json:"sha256"`
	Body   json.RawMessage `json:"body"`
}

func NewRequestArtifact(format string, body []byte) (RequestArtifact, error) {
	if !validID(format) || !json.Valid(body) {
		return RequestArtifact{}, fmt.Errorf("approval request artifact requires an explicit format and valid JSON")
	}
	// RawMessage normalization preserves exact numeric tokens without float64.
	// It also survives storage's whitespace removal and HTML escaping unchanged.
	canonical, err := json.Marshal(json.RawMessage(body))
	if err != nil {
		return RequestArtifact{}, err
	}
	hash := sha256.Sum256(canonical)
	return RequestArtifact{format, hex.EncodeToString(hash[:]), canonical}, nil
}
func (a RequestArtifact) Validate() error {
	normalized, err := NewRequestArtifact(a.Format, a.Body)
	if err != nil || normalized.Hash != a.Hash || string(normalized.Body) != string(a.Body) {
		return fmt.Errorf("approval request artifact digest or canonical bytes mismatch")
	}
	return nil
}

type FileDigest struct {
	Token string `json:"token"`
	Hash  string `json:"sha256"`
	Size  int64  `json:"size"`
}

// ReviewProof retains the actual checked facts/outcome and original provider
// pins. Original bytes and temporary attachment URLs are replaced by digests.
// Snapshot.Revision still names the original complete AI request, not a freshly
// fingerprinted request with missing binary content.
type ReviewProof struct {
	Snapshot review.Request `json:"snapshot"`
	Outcome  review.Outcome `json:"outcome"`
	Files    []FileDigest   `json:"files"`
}

func NewReviewProof(request review.Request, outcome review.Outcome) (ReviewProof, error) {
	revision, err := review.Fingerprint(request)
	if err != nil || revision != request.Revision || outcome.Validate() != nil {
		return ReviewProof{}, ErrReviewNotCurrent
	}
	proof := ReviewProof{Snapshot: request, Outcome: outcome, Files: []FileDigest{}}
	// Read timestamps are not AI facts and change on every source inspection.
	proof.Snapshot.Document.StartTime = time.Time{}
	proof.Snapshot.Transactions = review.CanonicalTransactionEvidence(request.Transactions)
	proof.Snapshot.Document.Invoices = append([]aggregate.Invoice(nil), request.Document.Invoices...)
	for n := range proof.Snapshot.Document.Invoices {
		invoice := &proof.Snapshot.Document.Invoices[n]
		if len(invoice.Attachment.Data) == 0 {
			return ReviewProof{}, ErrReviewNotCurrent
		}
		hash := sha256.Sum256(invoice.Attachment.Data)
		proof.Files = append(proof.Files, FileDigest{invoice.FileToken, hex.EncodeToString(hash[:]), int64(len(invoice.Attachment.Data))})
		invoice.Candidates = slices.Clone(invoice.Candidates)
		sort.Slice(invoice.Candidates, func(i, j int) bool { return invoice.Candidates[i].RecordID < invoice.Candidates[j].RecordID })
		invoice.Attachment.Data = nil
		invoice.Attachment.URL = ""
	}
	proof.Snapshot.Document.Findings = slices.Clone(request.Document.Findings)
	sort.Slice(proof.Snapshot.Document.Findings, func(i, j int) bool {
		a, b := proof.Snapshot.Document.Findings[i], proof.Snapshot.Document.Findings[j]
		if a.CurrentSourceKey != b.CurrentSourceKey {
			return a.CurrentSourceKey < b.CurrentSourceKey
		}
		if a.PriorRecordID != b.PriorRecordID {
			return a.PriorRecordID < b.PriorRecordID
		}
		if a.PriorSourceKey != b.PriorSourceKey {
			return a.PriorSourceKey < b.PriorSourceKey
		}
		return a.Code < b.Code
	})
	sort.Slice(proof.Snapshot.Document.Invoices, func(i, j int) bool {
		return proof.Snapshot.Document.Invoices[i].FileToken < proof.Snapshot.Document.Invoices[j].FileToken
	})
	sort.Slice(proof.Files, func(i, j int) bool { return proof.Files[i].Token < proof.Files[j].Token })
	return proof, nil
}

type PreparedPlan struct {
	Plan    Plan            `json:"plan"`
	Request RequestArtifact `json:"request"`
}

// PreparedBatch is an immutable, private audit of the whole explicit selection.
// It is neither a member reservation nor evidence that an instance was created.
type PreparedBatch struct {
	ID         string         `json:"id"`
	Plans      []PreparedPlan `json:"plans"`
	Reviews    []ReviewProof  `json:"reviews"`
	PreparedAt time.Time      `json:"prepared_at"`
}

func NewPreparedBatch(plans []PreparedPlan, proofs []ReviewProof) (PreparedBatch, error) {
	batch := PreparedBatch{Plans: plans, Reviews: proofs, PreparedAt: time.Now().UTC()}
	// Freeze caller maps/slices and opaque request bytes, without binary copies.
	raw, err := json.Marshal(batch)
	if err != nil {
		return PreparedBatch{}, err
	}
	if err = json.Unmarshal(raw, &batch); err != nil {
		return PreparedBatch{}, err
	}
	sort.Slice(batch.Plans, func(i, j int) bool { return batch.Plans[i].Plan.ID < batch.Plans[j].Plan.ID })
	sort.Slice(batch.Reviews, func(i, j int) bool {
		return batch.Reviews[i].Snapshot.Document.RecordID < batch.Reviews[j].Snapshot.Document.RecordID
	})
	if err := batch.validateContents(); err != nil {
		return PreparedBatch{}, err
	}
	batch.ID, err = batch.identity()
	return batch, err
}
func (b PreparedBatch) identity() (string, error) {
	raw, err := json.Marshal(struct {
		Plans   []PreparedPlan
		Reviews []ReviewProof
	}{b.Plans, b.Reviews})
	if err != nil {
		return "", err
	}
	if len(raw) > 8<<20 {
		return "", fmt.Errorf("approval preparation exceeds private audit size limit")
	}
	hash := sha256.Sum256(append([]byte("approval-preparation-v1:"), raw...))
	return hex.EncodeToString(hash[:]), nil
}
func (b PreparedBatch) validateContents() error {
	if len(b.Plans) == 0 || b.PreparedAt.IsZero() {
		return fmt.Errorf("approval preparation requires plans and observation time")
	}
	first := b.Plans[0].Plan
	rows := map[string]Row{}
	for n, prepared := range b.Plans {
		p := prepared.Plan
		if p.Validate() != nil || prepared.Request.Validate() != nil || p.SourceScope != first.SourceScope || p.TargetScope != first.TargetScope || p.Template != first.Template || p.ConfigurationVersion != first.ConfigurationVersion || p.Submitter != first.Submitter || p.DepartmentID != first.DepartmentID || (n > 0 && b.Plans[n-1].Plan.ID >= p.ID) {
			return fmt.Errorf("approval preparation contains inconsistent plans or requests")
		}
		policyA, _ := json.Marshal(struct{ Axes, Allowed []string }{p.Axes, p.AllowedDecisions})
		policyB, _ := json.Marshal(struct{ Axes, Allowed []string }{first.Axes, first.AllowedDecisions})
		if string(policyA) != string(policyB) {
			return ErrConflict
		}
		for _, row := range p.Rows {
			if rows[row.RecordID].RecordID != "" {
				return ErrConflict
			}
			rows[row.RecordID] = row
		}
	}
	if len(b.Reviews) != len(rows) {
		return fmt.Errorf("approval preparation requires exactly one checked AI proof per member")
	}
	for n, proof := range b.Reviews {
		r := proof.Snapshot
		row, ok := rows[r.Document.RecordID]
		if !ok || (n > 0 && b.Reviews[n-1].Snapshot.Document.RecordID >= r.Document.RecordID) || r.LogicalID != first.SourceScope+":"+row.RecordID || r.Document.DocumentID != row.Review.DocumentID || r.Revision != row.Review.Revision || !validID(r.Provider) || !validID(r.ProviderVersion) || proof.Outcome.Validate() != nil || proof.Outcome.Decision != row.Review.Decision || len(proof.Files) == 0 || len(proof.Files) != len(r.Document.Invoices) {
			return ErrReviewNotCurrent
		}
		for k, file := range proof.Files {
			invoice := r.Document.Invoices[k]
			hash, err := hex.DecodeString(file.Hash)
			if !validID(file.Token) || file.Size <= 0 || err != nil || len(hash) != sha256.Size || file.Hash != hex.EncodeToString(hash) || (k > 0 && proof.Files[k-1].Token >= file.Token) || invoice.FileToken != file.Token || len(invoice.Attachment.Data) != 0 || invoice.Attachment.URL != "" {
				return fmt.Errorf("approval preparation contains invalid file proof or original binary/URL")
			}
		}
	}
	return nil
}
func (b PreparedBatch) Validate() error {
	if err := b.validateContents(); err != nil {
		return err
	}
	id, err := b.identity()
	if err != nil {
		return err
	}
	if id != b.ID {
		return fmt.Errorf("approval preparation digest mismatch")
	}
	return nil
}
func (b PreparedBatch) RecordIDs() []string {
	ids := make([]string, 0, len(b.Reviews))
	for _, proof := range b.Reviews {
		ids = append(ids, proof.Snapshot.Document.RecordID)
	}
	return ids
}
