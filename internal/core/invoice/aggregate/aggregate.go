// Package aggregate builds a provider-independent reimbursement document from
// all recognized attachments belonging to one Feishu detail record.
package aggregate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

type Invoice struct {
	FileToken      string
	LedgerRecordID string
	Attachment     invoice.Attachment
	Recognition    invoice.Recognition
	Facts          dupcheck.Invoice
	Candidates     []dupcheck.Invoice
}

type Document struct {
	DocumentID string
	DocumentSN string
	RecordID   string
	StartTime  time.Time
	Invoices   []Invoice
	Findings   []dupcheck.Finding
}

// Build checks that every attachment has a matching ledger row, sorts invoices
// for stable downstream mapping, and annotates possible duplicate evidence.
func Build(documentID, documentSN, recordID string, startTime time.Time, invoices []Invoice) (Document, error) {
	if strings.TrimSpace(documentID) == "" || strings.TrimSpace(documentSN) == "" || strings.TrimSpace(recordID) == "" || startTime.Unix() <= 0 {
		return Document{}, fmt.Errorf("reimbursement identity and start time are required")
	}
	if len(invoices) == 0 {
		return Document{}, fmt.Errorf("reimbursement has no recognized attachments")
	}
	copyInvoices := append([]Invoice(nil), invoices...)
	sort.Slice(copyInvoices, func(i, j int) bool { return copyInvoices[i].FileToken < copyInvoices[j].FileToken })
	result := Document{DocumentID: documentID, DocumentSN: documentSN, RecordID: recordID, StartTime: startTime, Invoices: copyInvoices}
	for index, invoice := range copyInvoices {
		if invoice.FileToken == "" || invoice.LedgerRecordID == "" || invoice.Facts.SourceKey != recordID+":"+invoice.FileToken || len(invoice.Recognition.Raw) == 0 {
			return Document{}, fmt.Errorf("attachment %d is not fully written to the invoice ledger", index+1)
		}
		if index > 0 && copyInvoices[index-1].FileToken == invoice.FileToken {
			return Document{}, fmt.Errorf("duplicate attachment token in reimbursement")
		}
		result.Findings = append(result.Findings, dupcheck.Compare(invoice.Facts, invoice.Candidates)...)
	}
	return result, nil
}
