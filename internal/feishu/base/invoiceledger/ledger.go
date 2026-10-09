// Package ledger maps receipt recognition results to the invoice ledger.
package invoiceledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/app/recognition"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	"github.com/yunyingk/twc-approval-go-bridge/internal/receiptcompat"
)

// Field IDs are stable even when a user renames a Bitable column.
const (
	AttachmentKey     = "attachment_key"
	SourceKey         = "source_key"
	RawJSON           = "raw_json"
	DetailID          = "detail_id"
	Relation          = "relation"
	TraceID           = "trace_id"
	UniqueKey         = "unique_key"
	RecognitionStatus = "recognition_status"
	OriginAttachment  = "origin_attachment"
	Title             = "title"
	Number            = "invoice_number"
	ReceiptType       = "receipt_type"
	BusinessCategory  = "business_category"
	Seller            = "seller"
	Buyer             = "buyer"
	Currency          = "currency"
	Pretax            = "pretax_amount"
	Tax               = "tax_amount"
	TaxRate           = "tax_rate"
	Total             = "total_amount"
	IssueDate         = "issue_date"
	Country           = "country"
	AISummary         = "ai_summary"
)

func normalizeLedgerSemantic(semantic string) string {
	for _, prefix := range []string{"ocr_", "bridge_", "feishu_"} {
		if strings.HasPrefix(semantic, prefix) {
			return strings.TrimPrefix(semantic, prefix)
		}
	}
	return semantic
}

type Config struct {
	BaseToken           string
	SourceTableID       string
	SourceDetailFieldID string
	TableID             string
	Fields              map[string]string // semantic name -> ledger field ID
}

type Store interface {
	ReadTextField(context.Context, string, string, string, string) (string, error)
	UpsertLedgerRecord(context.Context, string, string, string, string, map[string]any) (string, bool, error)
}

type Handler struct {
	config Config
	store  Store
	logger *slog.Logger
}

func New(config Config, store Store, logger *slog.Logger) (*Handler, error) {
	if config.BaseToken == "" || config.SourceTableID == "" || config.TableID == "" || store == nil {
		return nil, fmt.Errorf("ledger requires Base, source table, ledger table, and store")
	}
	normalizedFields := make(map[string]string, len(config.Fields))
	for k, v := range config.Fields {
		normalizedFields[normalizeLedgerSemantic(k)] = v
		normalizedFields[k] = v
	}
	keyField := normalizedFields[AttachmentKey]
	if keyField == "" {
		keyField = normalizedFields[SourceKey]
	}
	if keyField == "" || normalizedFields[RawJSON] == "" {
		return nil, fmt.Errorf("ledger requires attachment-key and raw-JSON field IDs")
	}
	normalizedFields[AttachmentKey] = keyField
	normalizedFields[SourceKey] = keyField
	config.Fields = normalizedFields

	if config.TableID == config.SourceTableID {
		return nil, fmt.Errorf("ledger table must differ from the attachment source table")
	}
	if config.Fields[DetailID] != "" && config.SourceDetailFieldID == "" {
		return nil, fmt.Errorf("ledger detail ID mapping requires a source detail field ID")
	}
	used := make(map[string]string, len(config.Fields))
	for semantic, fieldID := range config.Fields {
		norm := normalizeLedgerSemantic(semantic)
		switch norm {
		case AttachmentKey, SourceKey, RawJSON, DetailID, Relation, "detail_relation", TraceID, UniqueKey, RecognitionStatus, OriginAttachment, Title, Number, ReceiptType, BusinessCategory, Seller, Buyer, Currency, Pretax, Tax, TaxRate, Total, IssueDate, Country, AISummary, "summary":
		default:
			return nil, fmt.Errorf("unsupported ledger field mapping %q", semantic)
		}
		if fieldID == "" {
			continue
		}
		if previous := used[fieldID]; previous != "" && previous != norm && previous != semantic {
			// allow alias of same semantic concept
			continue
		}
		used[fieldID] = norm
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{config: config, store: store, logger: logger}, nil
}

func (h *Handler) Handle(ctx context.Context, result recognition.Result) error {
	if result.BaseToken != h.config.BaseToken || result.TableID != h.config.SourceTableID || result.RecordID == "" || result.FileToken == "" {
		return fmt.Errorf("receipt result does not match configured ledger source")
	}
	result.Recognition = receiptcompat.Normalize(result.Recognition)
	facts := result.Recognition.Facts
	sourceKey := result.RecordID + ":" + result.FileToken
	fields := make(map[string]any)
	put := func(semantic string, value any) {
		if id := h.config.Fields[semantic]; id != "" && value != nil && value != "" {
			fields[id] = value
		}
	}
	raw := result.Recognition.Raw
	if len(raw) == 0 {
		var err error
		raw, err = json.Marshal(map[string]any{"outputs": result.Recognition.Outputs, "summary": result.Recognition.Summary})
		if err != nil {
			return fmt.Errorf("encode receipt recognition: %w", err)
		}
	}
	if !json.Valid(raw) {
		return fmt.Errorf("receipt recognition contains invalid JSON")
	}
	put(RawJSON, string(raw))
	put("bridge_raw_json", string(raw))
	put(Relation, []string{result.RecordID})
	put("detail_relation", []string{result.RecordID})
	put("feishu_detail_relation", []string{result.RecordID})
	traceID := result.Recognition.Origin.TraceID
	if traceID == "" {
		traceID = traceIDFromRaw(raw)
	}
	if traceID == "" {
		hash := sha256.Sum256([]byte(sourceKey))
		traceID = "OCR-" + hex.EncodeToString(hash[:8])
	}
	put(TraceID, traceID)
	put("ocr_trace_id", traceID)
	put(UniqueKey, traceID)
	put("bridge_unique_key", traceID)

	put(AttachmentKey, sourceKey)
	put("bridge_attachment_key", sourceKey)
	put(SourceKey, sourceKey)
	put("bridge_source_key", sourceKey)

	put(Title, facts.Title)
	put(Number, facts.Number)
	put(ReceiptType, facts.ReceiptType)
	put(BusinessCategory, facts.BusinessCategory)
	put(Seller, facts.Seller)
	put(Buyer, facts.Buyer)
	put(Currency, strings.ToUpper(facts.Currency))
	put(TaxRate, facts.TaxRate)
	put(Country, facts.Country)
	put(AISummary, strings.TrimSpace(result.Recognition.Summary))
	put("summary", strings.TrimSpace(result.Recognition.Summary))
	put("ocr_summary", strings.TrimSpace(result.Recognition.Summary))
	put(RecognitionStatus, "已识别")
	put("bridge_recognition_status", "已识别")
	for _, item := range []struct{ semantic, value string }{{Pretax, facts.Pretax}, {Tax, facts.Tax}, {Total, facts.Total}} {
		if amount, ok := parseAmount(item.value); ok {
			put(item.semantic, amount)
		}
	}
	if date, ok := parseDate(facts.IssueDate); ok {
		put(IssueDate, date)
	}
	if h.config.Fields[DetailID] != "" {
		detailID, err := h.store.ReadTextField(ctx, result.BaseToken, result.TableID, result.RecordID, h.config.SourceDetailFieldID)
		if err != nil {
			return fmt.Errorf("read source detail ID: %w", err)
		}
		put(DetailID, detailID)
	}
	keyFieldID := h.config.Fields[AttachmentKey]
	if keyFieldID == "" {
		keyFieldID = h.config.Fields[SourceKey]
	}
	var id string
	var created bool
	var err error
	if store, ok := h.store.(interface {
		UpsertRecognizedInvoice(context.Context, string, string, string, string, map[string]any) (string, bool, error)
	}); ok {
		id, created, err = store.UpsertRecognizedInvoice(ctx, result.BaseToken, h.config.TableID, keyFieldID, sourceKey, fields)
	} else {
		id, created, err = h.store.UpsertLedgerRecord(ctx, result.BaseToken, h.config.TableID, keyFieldID, sourceKey, fields)
	}
	if err != nil {
		return fmt.Errorf("write invoice ledger: %w", err)
	}
	h.logger.InfoContext(ctx, "receipt ledger written", "record_id", id, "created", created, "source_record_id", result.RecordID)
	return nil
}

func traceIDFromRaw(raw json.RawMessage) string {
	var response struct {
		TraceID json.RawMessage `json:"traceId"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return ""
	}
	var traceID string
	if err := json.Unmarshal(response.TraceID, &traceID); err != nil {
		return ""
	}
	return strings.TrimSpace(traceID)
}

func parseAmount(value string) (json.Number, bool) { return invoice.Decimal(value) }

func parseDate(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return 0, false
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006.01.02"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed.UnixMilli(), true
		}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UnixMilli(), true
	}
	return 0, false
}
