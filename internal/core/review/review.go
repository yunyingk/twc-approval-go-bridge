// Package review defines the shared audit contract; provider protocols stay outside it.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
)

var ErrRequestRejected = errors.New("review request was explicitly rejected by provider")

// These source lifecycle states stop pending submission without discarding
// invoice history. Permission, network and incomplete-ledger errors are distinct.
var ErrNoAttachments = errors.New("review source has no attachments")
var ErrSourceRemoved = errors.New("review source record was removed")
var ErrLedgerIncomplete = errors.New("invoice ledger is incomplete")
var ErrLedgerAmbiguous = errors.New("invoice ledger has conflicting source rows")

type File struct {
	Token      string
	Attachment invoice.Attachment
}
type Detail struct {
	DocumentID, DocumentSN, RecordID string
	StartTime                        time.Time
	Files                            []File
	Context                          map[string]string
	Transactions                     *TransactionEvidence
}
type LedgerEntry struct {
	RecordID    string
	Recognition invoice.Recognition
	Facts       dupcheck.Invoice
}

type Request struct {
	Document        aggregate.Document   `json:"document"`
	LogicalID       string               `json:"logical_id"`
	Revision        string               `json:"revision"`
	Provider        string               `json:"provider"`
	ProviderVersion string               `json:"provider_version,omitempty"`
	RulesVersion    string               `json:"rules_version,omitempty"`
	Context         map[string]string    `json:"context,omitempty"`
	Transactions    *TransactionEvidence `json:"transactions,omitempty"`
}

type Outcome struct {
	Decision   string `json:"decision"`
	Comment    string `json:"comment"`
	ExternalID string `json:"external_id,omitempty"`
	URL        string `json:"url,omitempty"`
}

func (o Outcome) Validate() error {
	if o.Decision != "approve" && o.Decision != "reject" && o.Decision != "review" {
		return fmt.Errorf("invalid review decision")
	}
	return nil
}

type Submission struct {
	DocumentID          string   `json:"document_id"`
	Status              string   `json:"status"` // pending or completed; failures are attempt states.
	Outcome             *Outcome `json:"outcome,omitempty"`
	AttachmentCount     int      `json:"attachment_count"`
	AttachmentFields    int      `json:"attachment_fields"`
	StructuredInvoices  int      `json:"structured_invoices"`
	DuplicateCandidates int      `json:"duplicate_candidates"`
	AcceptedInvoices    int      `json:"accepted_invoices"`
}
type Attempt struct {
	Request    Request       `json:"request"`
	State      string        `json:"state"` // submitting, unknown, failed, pending, completed
	Submission *Submission   `json:"submission,omitempty"`
	Delivered  bool          `json:"delivered"`
	Failure    *FailureInfo  `json:"failure,omitempty"`
	Delivery   *DeliveryInfo `json:"delivery,omitempty"`
}

// FailureInfo stores safe classifications, never upstream response bodies,
// credentials, error strings or receipt facts.
type FailureInfo struct {
	Phase      string    `json:"phase"`
	Code       string    `json:"code"`
	HTTPStatus int       `json:"http_status,omitempty"`
	RemoteCode string    `json:"remote_code,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type DeliveryInfo struct {
	State     string    `json:"state"` // delivered, superseded, check_failed, write_failed
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Fingerprint excludes fetch timestamps and temporary URLs. Content, effective
// facts, context and the selected implementation determine an immutable revision.
func Fingerprint(r Request) (string, error) {
	type item struct {
		Token, ContentHash string
		Facts              *invoice.Facts
		Origin             invoice.Origin
		Summary            string
		Raw                json.RawMessage
		Evidence           []dupcheck.Invoice
	}
	items := make([]item, 0, len(r.Document.Invoices))
	for _, in := range r.Document.Invoices {
		digest := sha256.Sum256(in.Attachment.Data)
		evidence := append([]dupcheck.Invoice(nil), in.Candidates...)
		sort.Slice(evidence, func(i, j int) bool { return evidence[i].RecordID < evidence[j].RecordID })
		items = append(items, item{in.FileToken, hex.EncodeToString(digest[:]), in.Recognition.Facts, in.Recognition.Origin, in.Recognition.Summary, in.Recognition.Raw, evidence})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Token < items[j].Token })
	data, err := json.Marshal(struct {
		LogicalID, DocumentSN, Provider, ProviderVersion, RulesVersion string
		Context                                                        map[string]string
		Items                                                          []item
		Transactions                                                   *TransactionEvidence `json:",omitempty"`
	}{r.LogicalID, r.Document.DocumentSN, r.Provider, r.ProviderVersion, r.RulesVersion, r.Context, items, canonicalTransactions(r.Transactions)})
	if err != nil {
		return "", fmt.Errorf("encode review revision: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
