package review

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
)

type fakeSource struct {
	missing bool
}

func (s fakeSource) ReadDetail(context.Context, string) (Detail, error) {
	return Detail{DocumentID: "detail-1", DocumentSN: "SN-1", RecordID: "rec1", StartTime: time.Unix(100, 0),
		Files: []File{{Token: "b", Attachment: receipt.Attachment{Name: "b.jpg", ContentType: "image/jpeg", Data: []byte("b")}},
			{Token: "a", Attachment: receipt.Attachment{Name: "a.jpg", ContentType: "image/jpeg", Data: []byte("a")}}}}, nil
}

func (s fakeSource) ReadLedgerEntry(_ context.Context, key string) (LedgerEntry, error) {
	if s.missing && key == "rec1:b" {
		return LedgerEntry{}, errors.New("missing OCR")
	}
	return LedgerEntry{RecordID: "ledger-" + key,
		Recognition: receipt.Recognition{Raw: json.RawMessage(`{"outputs":{"Number":"N-1"}}`),
			Outputs: map[string]json.RawMessage{"Number": json.RawMessage(`"N-1"`),
				"currency": json.RawMessage(`"USD"`), "total": json.RawMessage(`"10"`),
				"Seller": json.RawMessage(`"Shop"`), "Buyer": json.RawMessage(`"Buyer"`)}},
		Facts: dupcheck.Invoice{SourceKey: key, Number: "N-1", Seller: "Shop", Type: "invoice"}}, nil
}

func (s fakeSource) FindInvoiceCandidates(context.Context, string) ([]dupcheck.Invoice, error) {
	return []dupcheck.Invoice{{RecordID: "old", SourceKey: "old:file", Number: "N-1", Seller: "Shop", Type: "invoice"}}, nil
}

type fakeSeal struct {
	uploads []string
	submits []seal.DocumentRequest
}

func (s *fakeSeal) UploadAttachment(_ context.Context, file seal.Attachment) (seal.UploadResponse, error) {
	s.uploads = append(s.uploads, file.Name)
	return seal.UploadResponse{AttachmentID: file.Name}, nil
}

func (s *fakeSeal) SubmitDocument(_ context.Context, document seal.DocumentRequest) (seal.SubmitResponse, error) {
	s.submits = append(s.submits, document)
	return seal.SubmitResponse{Success: true}, nil
}

func TestSubmitAggregatesTwoLedgerInvoicesAndUploadsBothOriginals(t *testing.T) {
	gateway := &fakeSeal{}
	service, _ := New(fakeSource{}, gateway)
	result, err := service.Submit(context.Background(), "rec1")
	if err != nil {
		t.Fatal(err)
	}
	if result.AttachmentCount != 2 || result.StructuredInvoices != 2 || result.DuplicateCandidates != 2 ||
		len(gateway.uploads) != 2 || len(gateway.submits) != 1 || len(gateway.submits[0].Invoices) != 2 ||
		gateway.uploads[0] != "a.jpg" || gateway.uploads[1] != "b.jpg" {
		t.Fatalf("unexpected result: %#v uploads=%v submits=%v", result, gateway.uploads, gateway.submits)
	}
}

func TestSubmitDoesNotUploadPartialLedger(t *testing.T) {
	gateway := &fakeSeal{}
	service, _ := New(fakeSource{missing: true}, gateway)
	if _, err := service.Submit(context.Background(), "rec1"); err == nil {
		t.Fatal("missing ledger row must stop submission")
	}
	if len(gateway.uploads) != 0 || len(gateway.submits) != 0 {
		t.Fatal("partial reimbursement must not be sent")
	}
}
