package review

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type fakeSource struct {
	missing      bool
	transactions *core.TransactionEvidence
}

func (s fakeSource) ReadDetail(context.Context, string) (core.Detail, error) {
	return core.Detail{DocumentID: "detail-1", DocumentSN: "SN-1", RecordID: "rec1",
		RecordURL: "https://feishu.cn/base/base1?table=tbl1&record=rec1", StartTime: time.Unix(100, 0),
		Files: []core.File{{Token: "b", Attachment: invoice.Attachment{Name: "b.jpg", ContentType: "image/jpeg", Data: []byte("b")}},
			{Token: "a", Attachment: invoice.Attachment{Name: "a.jpg", ContentType: "image/jpeg", Data: []byte("a")}}}, Transactions: s.transactions}, nil
}

func (s fakeSource) ReadLedgerEntry(_ context.Context, key string) (core.LedgerEntry, error) {
	if s.missing && key == "rec1:b" {
		return core.LedgerEntry{}, errors.New("missing OCR")
	}
	return core.LedgerEntry{RecordID: "ledger-" + key,
		Recognition: invoice.Recognition{Raw: json.RawMessage(`{"outputs":{"Number":"N-1"}}`),
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
	return seal.UploadResponse{AttachmentID: file.Name, Attachment: seal.AttachmentInfo{
		Name: file.Name, MimeType: file.ContentType, URL: "https://example.com/file",
		OSSPath: "path", OSSSignedURL: "https://example.com/signed", OSSFileSize: int64(len(file.Data)),
	}}, nil
}

func (s *fakeSeal) SubmitDocument(_ context.Context, document seal.DocumentRequest) (seal.SubmitResponse, error) {
	s.submits = append(s.submits, document)
	return seal.SubmitResponse{Success: true}, nil
}

func TestSubmitAggregatesTwoLedgerInvoicesAndUploadsBothOriginals(t *testing.T) {
	gateway := &fakeSeal{}
	service := testService(t, fakeSource{}, gateway)
	result, err := service.Submit(context.Background(), "rec1")
	if err != nil {
		t.Fatal(err)
	}
	if result.AttachmentCount != 2 || result.StructuredInvoices != 2 || result.DuplicateCandidates != 2 ||
		len(gateway.uploads) != 2 || len(gateway.submits) != 1 || len(gateway.submits[0].Invoices) != 2 ||
		gateway.uploads[0] != "a.jpg" || gateway.uploads[1] != "b.jpg" {
		t.Fatalf("unexpected result: %#v uploads=%v submits=%v", result, gateway.uploads, gateway.submits)
	}
	expectedURL := "https://feishu.cn/base/base1?table=tbl1&record=rec1"
	if gateway.submits[0].DocumentURL != expectedURL {
		t.Fatalf("expected DocumentURL %q, got %q", expectedURL, gateway.submits[0].DocumentURL)
	}
	foundURLField := false
	for _, f := range gateway.submits[0].Fields {
		if f.Key == "source_record_url" && f.Value == expectedURL {
			foundURLField = true
			break
		}
	}
	if !foundURLField {
		t.Fatalf("expected source_record_url field with value %q", expectedURL)
	}
}

func TestSubmitDoesNotUploadPartialLedger(t *testing.T) {
	gateway := &fakeSeal{}
	service := testService(t, fakeSource{missing: true}, gateway)
	if _, err := service.Submit(context.Background(), "rec1"); err == nil {
		t.Fatal("missing ledger row must stop submission")
	}
	if len(gateway.uploads) != 0 || len(gateway.submits) != 0 {
		t.Fatal("partial reimbursement must not be sent")
	}
}

func TestGatewayPreservesSharedPaymentEvidenceWithoutInventingClaims(t *testing.T) {
	evidence := &core.TransactionEvidence{Source: "payments", LinkedRecordIDs: []string{"payment"}, Transactions: []core.Transaction{{RecordID: "payment", OriginalAmount: "9007199254740993.01", OriginalCurrency: "USD", BookedAmountCNY: "700.00"}}, Issues: []core.EvidenceIssue{{Code: "missing_merchant"}}}
	gateway := &fakeSeal{}
	service := testService(t, fakeSource{transactions: evidence}, gateway)
	if _, err := service.Submit(context.Background(), "rec1"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range gateway.submits[0].Fields {
		if field.Key == "bridge_transaction_evidence" {
			var got core.TransactionEvidence
			if err := json.Unmarshal([]byte(field.Value.(string)), &got); err != nil {
				t.Fatal(err)
			}
			if got.Source != evidence.Source || got.Transactions[0].OriginalAmount != "9007199254740993.01" || len(got.Issues) != 1 {
				t.Fatal("payment evidence lost precision, provenance or missing-data flags")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("Seal request omitted common payment snapshot")
	}
	for _, invoice := range gateway.submits[0].Invoices {
		if invoice.ClaimedAmount.String() == "" {
			t.Fatal("structured invoice must have claimedAmount")
		}
		if len(invoice.ExpenseRefs) == 0 {
			t.Fatal("structured invoice must have expenseRefs")
		}
		for _, ref := range invoice.ExpenseRefs {
			if ref.SourceExpenseID == "" || ref.AllocatedAmount.String() == "" || ref.CurrencyCode == "" {
				t.Fatalf("incomplete expenseRef: %#v", ref)
			}
		}
	}

	// Verify fields align with proposal
	sub := gateway.submits[0]
	if len(sub.Fields) == 0 || sub.Fields[0].Key != "expense_details" || sub.Fields[0].SemanticType != "EXPENSE_DETAIL_LIST" {
		t.Fatalf("expected first field to be expense_details LIST, got %#v", sub.Fields[0])
	}
	hasReimbID := false
	hasTxAmt := false
	for _, f := range sub.Fields {
		if f.Key == "reimbursement_id" && f.Value == "SN-1" {
			hasReimbID = true
		}
		if f.Key == "transaction_amount" {
			if av, ok := f.Value.(seal.AmountValue); ok {
				if av.Currency == "USD" && av.Amount.(json.Number).String() == "9007199254740993.01" {
					hasTxAmt = true
				}
			}
		}
	}
	if !hasReimbID {
		t.Fatal("expected reimbursement_id field in sub.Fields")
	}
	if !hasTxAmt {
		t.Fatal("expected transaction_amount field with 9007199254740993.01 USD in sub.Fields")
	}
}

func testService(t *testing.T, source app.Source, client SealGateway) *app.Service {
	t.Helper()
	gateway, err := NewGateway(client)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.New(source, gateway, app.Options{Provider: "seal", Versioned: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
