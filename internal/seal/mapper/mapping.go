// Package mapper turns recognized receipts into Seal AI document requests.
// It is deliberately separate from both the Feishu adapter and the generic Seal client.
package mapper

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/dupcheck"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice/aggregate"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receiptcompat"
	"github.com/yunyingk/twc-approval-go-bridge/internal/seal"
)

// Input contains the facts available after OCR and a successful Seal upload.
// StartTime is supplied by the caller so retries and tests can control it.
type Input struct {
	DocumentID  string
	DocumentSN  string
	RecordID    string
	RecordURL   string
	FileToken   string
	FileName    string
	Recognition invoice.Recognition
	Upload      seal.UploadResponse
	StartTime   time.Time
}

// DocumentParams configures the complete document assembly for Seal AI.
type DocumentParams struct {
	Batch        aggregate.Document
	Uploads      map[string]seal.UploadResponse
	Context      map[string]string
	Transactions *core.TransactionEvidence
	Revision     string
}

// Map maps one receipt into a caller-identified reimbursement document.
// The caller can combine fields and invoices from several receipts before
// submitting that document.
func Map(input Input) (seal.DocumentRequest, error) {
	if strings.TrimSpace(input.DocumentID) == "" || strings.TrimSpace(input.DocumentSN) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("Seal document ID and SN are required")
	}
	if strings.TrimSpace(input.RecordID) == "" || strings.TrimSpace(input.FileToken) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("receipt source record ID and file token are required")
	}
	if strings.TrimSpace(input.Upload.AttachmentID) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("uploaded receipt attachment ID is required")
	}
	if !input.Upload.Valid() {
		return seal.DocumentRequest{}, fmt.Errorf("Seal attachment upload has not completed successfully")
	}
	if input.StartTime.IsZero() || input.StartTime.Unix() <= 0 {
		return seal.DocumentRequest{}, fmt.Errorf("document start time must be after Unix epoch")
	}

	normRec := receiptcompat.Normalize(input.Recognition)
	facts := normRec.Facts

	invItem := aggregate.Invoice{
		FileToken:      input.FileToken,
		LedgerRecordID: input.RecordID,
		Attachment: invoice.Attachment{
			Name: input.FileName,
		},
		Recognition: normRec,
		Facts: dupcheck.Invoice{
			RecordID:  input.RecordID,
			SourceKey: input.RecordID + ":" + input.FileToken,
			Number:    facts.Number,
			Seller:    facts.Seller,
			Type:      facts.ReceiptType,
			IssueDate: facts.IssueDate,
			Total:     facts.Total,
			Currency:  facts.Currency,
		},
	}

	batch := aggregate.Document{
		DocumentID: input.DocumentID,
		DocumentSN: input.DocumentSN,
		RecordID:   input.RecordID,
		RecordURL:  input.RecordURL,
		StartTime:  input.StartTime,
		Invoices:   []aggregate.Invoice{invItem},
	}

	uploads := map[string]seal.UploadResponse{
		input.FileToken: input.Upload,
	}

	return BuildDocument(DocumentParams{
		Batch:   batch,
		Uploads: uploads,
	})
}

// MapBatch adapts a provider-independent, multi-invoice reimbursement to one Seal document.
func MapBatch(batch aggregate.Document, uploads map[string]seal.UploadResponse) (seal.DocumentRequest, error) {
	return BuildDocument(DocumentParams{
		Batch:   batch,
		Uploads: uploads,
	})
}

// BuildDocument builds the complete, fully-aligned Seal AI document request.
// It follows the standard schema:
// 1. fields.expense_details: 2D array of invoices (semanticType: EXPENSE_DETAIL_LIST)
// 2. 24 single-value header/transaction fields
// 3. Structured invoices array with claimedAmount and expenseRefs
func BuildDocument(params DocumentParams) (seal.DocumentRequest, error) {
	batch := params.Batch
	if strings.TrimSpace(batch.DocumentID) == "" || strings.TrimSpace(batch.DocumentSN) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("Seal document ID and SN are required")
	}
	if strings.TrimSpace(batch.RecordID) == "" {
		return seal.DocumentRequest{}, fmt.Errorf("receipt source record ID is required")
	}
	if batch.StartTime.IsZero() || batch.StartTime.Unix() <= 0 {
		return seal.DocumentRequest{}, fmt.Errorf("document start time must be after Unix epoch")
	}

	doc := seal.DocumentRequest{
		DocumentID:        batch.DocumentID,
		DocumentSN:        batch.DocumentSN,
		DocumentURL:       batch.RecordURL,
		SourceDocumentURL: batch.RecordURL,
		StartTime:         batch.StartTime.Unix(),
		Fields:            make([]seal.DocumentField, 0, len(batch.Invoices)+30),
		Invoices:          make([]seal.ExternalInvoice, 0, len(batch.Invoices)),
	}

	findingMap := make(map[string]bool)
	for _, finding := range batch.Findings {
		findingMap[finding.CurrentSourceKey] = true
	}

	submitterName := getContext(params.Context, "reimbursement_submitter_name", "employee", "submitter", "reimbursement_submitter")
	submitterID := getContext(params.Context, "reimbursement_submitter_source_id", "employee_id", "submitter_id", "employee_source_id")

	// 1. Build expense_details 2D row array
	detailRows := make([][]seal.DocumentField, 0, len(batch.Invoices))
	for index, item := range batch.Invoices {
		upload, ok := params.Uploads[item.FileToken]
		if !ok || upload.AttachmentID == "" || !upload.Valid() {
			return seal.DocumentRequest{}, fmt.Errorf("Seal upload missing or invalid for attachment %d", index+1)
		}

		itemRec := receiptcompat.Normalize(item.Recognition)
		facts := itemRec.Facts

		sourceKey := batch.RecordID + ":" + item.FileToken
		detailID := item.LedgerRecordID
		if strings.TrimSpace(detailID) == "" {
			detailID = item.Facts.RecordID
		}
		if strings.TrimSpace(detailID) == "" {
			detailID = sourceKey
		}

		buyer := strings.TrimSpace(facts.Buyer)
		if buyer == "" && submitterName != "" {
			buyer = submitterName
		}

		dupRiskStatus := "正常"
		if findingMap[sourceKey] || len(item.Candidates) > 0 {
			dupRiskStatus = "疑似重复"
		}

		recStatus := "识别成功"
		if len(itemRec.Raw) == 0 {
			recStatus = "未识别"
		}

		row := []seal.DocumentField{
			{Key: "detailId", Label: "发票唯一键ID", Type: "TEXT", Value: detailID},
			{Key: "source_invoice_attachment_lookup", Label: "发票原件", Type: "ATTACHMENT", Value: []seal.AttachmentInfo{upload.Attachment}},
			{Key: "invoice_number", Label: "发票号", Type: "TEXT", Value: facts.Number},
			{Key: "invoice_type", Label: "票据类型", Type: "TEXT", Value: defaultIfEmpty(facts.ReceiptType, "invoice")},
			{Key: "seller", Label: "开票方", Type: "TEXT", Value: facts.Seller},
			{Key: "buyer", Label: "购买方", Type: "TEXT", Value: buyer},
			{Key: "expense_category", Label: "业务分类", Type: "TEXT", Value: facts.BusinessCategory},
			{Key: "item_amount", Label: "发票含税金额", Type: "AMOUNT", Value: parseAmount(facts.Total, facts.Currency)},
			{Key: "expense_date", Label: "开票日期", Type: "DATE", Value: formatDate(facts.IssueDate)},
			{Key: "purpose", Label: "发票摘要", Type: "TEXT", Value: facts.Title},
			{Key: "currency", Label: "发票币种", Type: "TEXT", Value: facts.Currency},
			{Key: "ai_expense_summary", Label: "AI消费概要", Type: "TEXT", Value: itemRec.Summary},
			{Key: "amount_without_tax", Label: "不含税金额", Type: "NUMBER", Value: parseNumber(facts.Pretax)},
			{Key: "linked_reimbursement_record", Label: "关联明细ID", Type: "TEXT", Value: fmt.Sprintf("[\"%s\"]", batch.RecordID)},
			{Key: "ocr_trace_id", Label: "发票识别流水号", Type: "TEXT", Value: itemRec.Origin.TraceID},
			{Key: "country", Label: "国家", Type: "TEXT", Value: facts.Country},
			{Key: "whitelist_hit", Label: "白名单命中", Type: "TEXT", Value: ""},
			{Key: "tax_rate", Label: "税率", Type: "NUMBER", Value: parseNumber(facts.TaxRate)},
			{Key: "tax_amount", Label: "税额", Type: "NUMBER", Value: parseNumber(facts.Tax)},
			{Key: "ledger_record_id", Label: "记录ID", Type: "TEXT", Value: item.LedgerRecordID},
			{Key: "ocr_raw_json", Label: "识别原始JSON", Type: "TEXT", Value: string(itemRec.Raw)},
			{Key: "recognition_status", Label: "识别状态", Type: "TEXT", Value: recStatus},
			{Key: "duplicate_description", Label: "重复描述", Type: "TEXT", Value: ""},
			{Key: "duplicate_risk_status", Label: "重复风险状态", Type: "TEXT", Value: dupRiskStatus},
			{Key: "attachment_identifier", Label: "附件标识", Type: "TEXT", Value: sourceKey},
		}

		detailRows = append(detailRows, row)

		// Map structured invoice
		if inv, ok := mapInvoiceItem(item, upload, batch.RecordID, detailID, submitterName); ok {
			doc.Invoices = append(doc.Invoices, inv)
		}
	}

	doc.Fields = append(doc.Fields, seal.DocumentField{
		Key:          "expense_details",
		Label:        "发票明细",
		Type:         "LIST",
		SemanticType: "EXPENSE_DETAIL_LIST",
		Value:        detailRows,
	})

	// 2. Build 24 single-value fields (reimbursement & transaction)
	var (
		txAmount       any = seal.AmountValue{Amount: "", Currency: ""}
		txTime         string
		expenseDate    string
		txCurrency     string
		txID           string
		merchant       string
		cardholderName string
		cardholderID   string
		cardLastFour   string
		settleAmount   any = seal.AmountValue{Amount: "", Currency: ""}
		settleCurrency string
		merchantRegion string
		txType         string
		paymentChannel string
		reconcilStatus string
		exchangeRate   any = ""
		txStatus       string
	)

	if params.Transactions != nil && len(params.Transactions.Transactions) > 0 {
		tx := params.Transactions.Transactions[0]
		txAmount = parseAmount(tx.OriginalAmount, tx.OriginalCurrency)
		txTime = formatDate(tx.OccurredAt)
		expenseDate = txTime
		txCurrency = tx.OriginalCurrency
		txID = tx.TransactionID
		merchant = tx.Merchant
		cardholderName = tx.CardholderName
		cardholderID = tx.CardholderSourceID
		cardLastFour = tx.CardLastFour

		sAmt := tx.SettlementAmount
		if sAmt == "" {
			sAmt = tx.BookedAmountCNY
		}
		sCurr := tx.SettlementCurrency
		if sCurr == "" && sAmt != "" {
			sCurr = "CNY"
		}
		settleAmount = parseAmount(sAmt, sCurr)
		settleCurrency = sCurr

		merchantRegion = tx.MerchantRegion
		if merchantRegion == "" {
			merchantRegion = tx.Country
		}
		txType = tx.TransactionType
		paymentChannel = tx.PaymentChannel
		reconcilStatus = tx.ReconciliationStatus
		exchangeRate = parseNumber(tx.ExchangeRate)
		txStatus = tx.Status
	}

	if expDateContext := formatDate(getContext(params.Context, "expense_date")); expDateContext != "" {
		expenseDate = expDateContext
	}
	if cardholderName == "" {
		cardholderName = getContext(params.Context, "cardholder_name", "cardholder")
	}
	if cardholderID == "" {
		cardholderID = getContext(params.Context, "cardholder_source_id")
	}
	if cardLastFour == "" {
		cardLastFour = getContext(params.Context, "card_last_four")
	}
	if merchantRegion == "" {
		merchantRegion = getContext(params.Context, "merchant_region")
	}
	if txType == "" {
		txType = getContext(params.Context, "transaction_type")
	}
	if paymentChannel == "" {
		paymentChannel = getContext(params.Context, "payment_channel")
	}
	if reconcilStatus == "" {
		reconcilStatus = getContext(params.Context, "reconciliation_status")
	}
	if txStatus == "" {
		txStatus = getContext(params.Context, "transaction_status")
	}

	expenseCategory := getContext(params.Context, "expense_category", "category")
	purpose := getContext(params.Context, "purpose", "expense_reason", "reason")
	projectNumber := getContext(params.Context, "project_number", "project_code", "project")
	projectRecordID := getContext(params.Context, "project_record_id")

	doc.Fields = append(doc.Fields, []seal.DocumentField{
		{Key: "reimbursement_id", Label: "报销明细ID", Type: "TEXT", Value: batch.DocumentSN},
		{Key: "reimbursement_submitter_name", Label: "报销人", Type: "TEXT", Value: submitterName},
		{Key: "reimbursement_submitter_source_id", Label: "报销人飞书人员ID", Type: "TEXT", Value: submitterID},
		{Key: "expense_category", Label: "报销费用类目", Type: "TEXT", Value: expenseCategory},
		{Key: "transaction_amount", Label: "易商卡交易金额", Type: "AMOUNT", Value: txAmount},
		{Key: "transaction_time", Label: "交易时间", Type: "DATE", Value: txTime},
		{Key: "expense_date", Label: "消费日期", Type: "DATE", Value: expenseDate},
		{Key: "purpose", Label: "消费事由", Type: "TEXT", Value: purpose},
		{Key: "transaction_currency", Label: "交易金额币种", Type: "TEXT", Value: txCurrency},
		{Key: "transaction_id", Label: "合思交易流水号", Type: "TEXT", Value: txID},
		{Key: "merchant", Label: "商户名称", Type: "TEXT", Value: merchant},
		{Key: "cardholder_name", Label: "持卡人", Type: "TEXT", Value: cardholderName},
		{Key: "cardholder_source_id", Label: "持卡人飞书人员ID", Type: "TEXT", Value: cardholderID},
		{Key: "card_last_four", Label: "卡号后四位", Type: "TEXT", Value: cardLastFour},
		{Key: "settlement_amount", Label: "结算金额", Type: "AMOUNT", Value: settleAmount},
		{Key: "settlement_currency", Label: "结算金额币种", Type: "TEXT", Value: settleCurrency},
		{Key: "merchant_region", Label: "商家地区", Type: "TEXT", Value: merchantRegion},
		{Key: "transaction_type", Label: "交易类型", Type: "TEXT", Value: txType},
		{Key: "payment_channel", Label: "支付渠道", Type: "TEXT", Value: paymentChannel},
		{Key: "reconciliation_status", Label: "核销状态", Type: "TEXT", Value: reconcilStatus},
		{Key: "exchange_rate", Label: "汇率", Type: "NUMBER", Value: exchangeRate},
		{Key: "transaction_status", Label: "交易状态", Type: "TEXT", Value: txStatus},
		{Key: "project_number", Label: "关联项目编号", Type: "TEXT", Value: projectNumber},
		{Key: "project_record_id", Label: "关联项目记录ID", Type: "TEXT", Value: projectRecordID},
	}...)

	// 3. Provenance and audit fields
	if batch.RecordURL != "" {
		doc.Fields = append(doc.Fields, seal.DocumentField{
			Key:   "source_record_url",
			Label: "单据原始链接",
			Type:  "TEXT",
			Value: batch.RecordURL,
		})
	}

	if params.Transactions != nil {
		rawTx, err := json.Marshal(params.Transactions)
		if err == nil {
			doc.Fields = append(doc.Fields, seal.DocumentField{
				Key:      "bridge_transaction_evidence",
				Label:    "关联支付流水及资料完整性",
				Type:     "TEXT",
				Value:    string(rawTx),
				Comments: "来源：交易流水表快照",
			})
		}
	}

	if params.Revision != "" {
		doc.Fields = append(doc.Fields, seal.DocumentField{
			Key:      "bridge_revision",
			Label:    "业务数据版本",
			Type:     "TEXT",
			Value:    params.Revision,
			Comments: "来源：数据快照哈希指纹",
		})
	}

	// 4. Duplicate candidates audit
	for index, finding := range batch.Findings {
		var prior dupcheck.Invoice
		for _, item := range batch.Invoices {
			if item.Facts.SourceKey != finding.CurrentSourceKey {
				continue
			}
			for _, candidate := range item.Candidates {
				if candidate.RecordID == finding.PriorRecordID && candidate.SourceKey == finding.PriorSourceKey {
					prior = candidate
					break
				}
			}
		}
		evidence, err := json.Marshal(struct {
			CurrentSourceKey string `json:"current_source_key"`
			PriorRecordID    string `json:"prior_record_id"`
			PriorSourceKey   string `json:"prior_source_key"`
			Code             string `json:"code"`
			Number           string `json:"invoice_number"`
			Seller           string `json:"seller"`
			Type             string `json:"receipt_type"`
			IssueDate        string `json:"issue_date"`
			Total            string `json:"total_amount"`
			Currency         string `json:"currency"`
		}{
			finding.CurrentSourceKey, finding.PriorRecordID, finding.PriorSourceKey, finding.Code,
			prior.Number, prior.Seller, prior.Type, prior.IssueDate, prior.Total, prior.Currency,
		})
		if err == nil {
			doc.Fields = append(doc.Fields, seal.DocumentField{
				Key:      fmt.Sprintf("duplicate_candidate_%02d", index+1),
				Label:    "台账查重候选",
				Type:     "TEXT",
				Value:    string(evidence),
				Comments: "来源：发票台账历史票号检索",
			})
		}
	}
	if len(batch.Findings) > 0 || len(batch.Invoices) > 0 {
		doc.Fields = append(doc.Fields, seal.DocumentField{
			Key:   "duplicate_evidence_scope",
			Label: "查重证据范围",
			Type:  "TEXT",
			Value: "候选来自按当前票号查询的来源记录；缺少票号时未执行查询。候选仅用于核对，不证明已报销或已结算；没有候选不证明不存在重复。检索未覆盖无票号、不同票号、其他来源或所有历史费用，未提供审批与结算状态。",
		})
	}

	return doc, nil
}

func mapInvoiceItem(item aggregate.Invoice, upload seal.UploadResponse, recordID, detailID, submitterName string) (seal.ExternalInvoice, bool) {
	facts := receiptcompat.Normalize(item.Recognition).Facts
	number := strings.TrimSpace(facts.Number)
	currency := strings.ToUpper(strings.TrimSpace(facts.Currency))
	total, totalOK := amount(facts.Total)
	seller := strings.TrimSpace(facts.Seller)
	buyer := strings.TrimSpace(facts.Buyer)
	if buyer == "" && strings.TrimSpace(submitterName) != "" {
		buyer = strings.TrimSpace(submitterName)
	}
	evidenceID := strings.TrimSpace(upload.AttachmentID)
	if number == "" || !currencyCode(currency) || !totalOK || seller == "" || buyer == "" || evidenceID == "" {
		return seal.ExternalInvoice{}, false
	}

	sourceInvoiceID := "feishu-invoice-" + detailID
	invoiceType := strings.ToLower(strings.TrimSpace(facts.ReceiptType))
	if invoiceType != "domestic" && invoiceType != "overseas" && invoiceType != "other" {
		invoiceType = "overseas"
	}

	inv := seal.ExternalInvoice{
		Kind:                 "invoice",
		SourceInvoiceID:      sourceInvoiceID,
		InvoiceType:          invoiceType,
		InvoiceNumber:        number,
		CurrencyCode:         currency,
		TotalAmount:          total,
		Seller:               seal.InvoiceParty{Name: seller},
		Buyer:                seal.InvoiceParty{Name: buyer},
		EvidenceAttachmentID: evidenceID,
		ClaimedAmount:        total,
		ExpenseRefs: []seal.ExpenseRef{
			{
				SourceExpenseID: detailID,
				AllocatedAmount: total,
				CurrencyCode:    currency,
			},
		},
	}
	if date := formatDate(facts.IssueDate); date != "" {
		inv.InvoiceDate = date
	}
	if tax, ok := amount(facts.Tax); ok {
		inv.TaxAmount = tax
	}
	if pretax, ok := amount(facts.Pretax); ok {
		inv.AmountWithoutTax = pretax
	}
	return inv, true
}

func amount(value string) (json.Number, bool) { return invoice.Decimal(value) }

func currencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func parseAmount(val, currency string) any {
	val = strings.TrimSpace(val)
	currency = strings.TrimSpace(currency)
	if num, ok := invoice.Decimal(val); ok {
		return seal.AmountValue{
			Amount:   num,
			Currency: currency,
		}
	}
	if val != "" {
		return seal.AmountValue{
			Amount:   val,
			Currency: currency,
		}
	}
	return seal.AmountValue{
		Amount:   "",
		Currency: currency,
	}
}

func parseNumber(val string) any {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}
	if num, ok := invoice.Decimal(val); ok {
		return num
	}
	return ""
}

func formatDate(val string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}
	if len(val) == 10 && val[4] == '-' && val[7] == '-' {
		return val
	}
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
		return t.Format("2006-01-02")
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006.01.02", "2006_01_02"} {
		if t, err := time.Parse(layout, val); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return val
}

func getContext(ctx map[string]string, keys ...string) string {
	if ctx == nil {
		return ""
	}
	for _, k := range keys {
		if v := strings.TrimSpace(ctx[k]); v != "" {
			return v
		}
	}
	return ""
}

func defaultIfEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}
