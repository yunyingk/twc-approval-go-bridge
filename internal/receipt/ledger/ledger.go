// Package ledger maps receipt recognition results to the invoice ledger.
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/receipt/flow"
)

// Field IDs are stable even when a user renames a Bitable column.
const (
	SourceKey        = "source_key"
	RawJSON          = "raw_json"
	DetailID         = "detail_id"
	Relation         = "relation"
	UniqueKey        = "unique_key"
	Title            = "title"
	Number           = "invoice_number"
	ReceiptType      = "receipt_type"
	BusinessCategory = "business_category"
	Seller           = "seller"
	Buyer            = "buyer"
	Currency         = "currency"
	Pretax           = "pretax_amount"
	Tax              = "tax_amount"
	TaxRate          = "tax_rate"
	Total            = "total_amount"
	IssueDate        = "issue_date"
	Country          = "country"
	AISummary        = "ai_summary"
)

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
	if config.BaseToken == "" || config.SourceTableID == "" || config.TableID == "" || config.Fields[SourceKey] == "" || config.Fields[RawJSON] == "" || store == nil {
		return nil, fmt.Errorf("ledger requires Base, source table, ledger table, source-key and raw-JSON field IDs, and store")
	}
	if config.TableID == config.SourceTableID {
		return nil, fmt.Errorf("ledger table must differ from the attachment source table")
	}
	if config.Fields[DetailID] != "" && config.SourceDetailFieldID == "" {
		return nil, fmt.Errorf("ledger detail ID mapping requires a source detail field ID")
	}
	used := make(map[string]string, len(config.Fields))
	for semantic, fieldID := range config.Fields {
		switch semantic {
		case SourceKey, RawJSON, DetailID, Relation, UniqueKey, Title, Number, ReceiptType, BusinessCategory, Seller, Buyer, Currency, Pretax, Tax, TaxRate, Total, IssueDate, Country, AISummary:
		default:
			return nil, fmt.Errorf("unsupported ledger field mapping %q", semantic)
		}
		if fieldID == "" {
			continue
		}
		if previous := used[fieldID]; previous != "" {
			return nil, fmt.Errorf("ledger field ID %s is assigned to both %s and %s", fieldID, previous, semantic)
		}
		used[fieldID] = semantic
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{config: config, store: store, logger: logger}, nil
}

func (h *Handler) Handle(ctx context.Context, result flow.Result) error {
	if result.BaseToken != h.config.BaseToken || result.TableID != h.config.SourceTableID || result.RecordID == "" || result.FileToken == "" {
		return fmt.Errorf("receipt result does not match configured ledger source")
	}
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
	put(Relation, []string{result.RecordID})
	uniqueKey := traceIDFromRaw(raw)
	if uniqueKey == "" {
		hash := sha256.Sum256([]byte(sourceKey))
		uniqueKey = "OCR-" + hex.EncodeToString(hash[:8])
	}
	put(UniqueKey, uniqueKey)
	put(Title, output(result, "title"))
	put(Number, output(result, "Number"))
	put(ReceiptType, output(result, "type"))
	put(BusinessCategory, output(result, "TypeofBill"))
	put(Seller, output(result, "Seller"))
	put(Buyer, output(result, "Buyer"))
	put(Currency, strings.ToUpper(output(result, "currency")))
	put(TaxRate, output(result, "taxrate"))
	put(Country, output(result, "country"))
	put(AISummary, strings.TrimSpace(result.Recognition.Summary))
	for _, item := range []struct{ semantic, outputKey string }{{Pretax, "amountwithouttax"}, {Tax, "tax"}, {Total, "total"}} {
		if amount, ok := parseAmount(output(result, item.outputKey)); ok {
			put(item.semantic, amount)
		}
	}
	if date, ok := parseDate(first(output(result, "DateofInssuance"), result.Recognition.Date)); ok {
		put(IssueDate, date)
	}
	if h.config.Fields[DetailID] != "" {
		detailID, err := h.store.ReadTextField(ctx, result.BaseToken, result.TableID, result.RecordID, h.config.SourceDetailFieldID)
		if err != nil {
			return fmt.Errorf("read source detail ID: %w", err)
		}
		put(DetailID, detailID)
	}
	id, created, err := h.store.UpsertLedgerRecord(ctx, result.BaseToken, h.config.TableID, h.config.Fields[SourceKey], sourceKey, fields)
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

func output(result flow.Result, key string) string {
	raw := result.Recognition.Outputs[key]
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseAmount(value string) (float64, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	if value == "" {
		return 0, false
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, false
	}
	return amount, true
}

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
