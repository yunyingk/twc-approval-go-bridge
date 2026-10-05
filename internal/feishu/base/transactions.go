package base

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// TransactionConfig follows a native relation, never a displayed transaction number.
type TransactionConfig struct {
	BaseToken, TableID, RelationFieldID string
	Fields                              map[string]string
}

func (s *ReviewSource) WithTransactions(binding TransactionConfig) (*ReviewSource, error) {
	if binding.BaseToken == "" || binding.TableID == "" || binding.RelationFieldID == "" || binding.BaseToken != s.base || binding.TableID == s.detailTable {
		return nil, fmt.Errorf("linked payment source requires a distinct table and relation in the same Base")
	}
	for _, semantic := range []string{"transaction_id", "original_amount", "original_currency", "merchant", "transaction_time"} {
		if binding.Fields[semantic] == "" {
			return nil, fmt.Errorf("linked payment field %s is required", semantic)
		}
	}
	fields := make(map[string]string, len(binding.Fields))
	for semantic, id := range binding.Fields {
		fields[semantic] = id
	}
	binding.Fields = fields
	s.transactions = &binding
	return s, nil
}

// ReadTransactions reads only schema and linked records. API/configuration
// failures stop preparation; missing source facts become explicit evidence issues.
func (s *ReviewSource) ReadTransactions(ctx context.Context, recordID string) (*core.TransactionEvidence, error) {
	if s.transactions == nil {
		return nil, nil
	}
	binding := s.transactions
	token, err := s.client.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	detailSchema, err := s.client.fieldSchema(ctx, token, s.base, s.detailTable)
	if err != nil {
		return nil, err
	}
	relation := detailSchema[binding.RelationFieldID]
	if (relation.Type != 18 && relation.Type != 21) || relation.RelatedTableID != binding.TableID {
		return nil, fmt.Errorf("configured transaction relation must link to the selected transaction table")
	}
	detail, err := s.client.recordFields(ctx, token, s.base, s.detailTable, recordID)
	if err != nil {
		return nil, err
	}
	ids, err := relatedRecordIDs(detail[relation.Name], binding.TableID)
	if err != nil {
		return nil, err
	}
	evidence := &core.TransactionEvidence{Source: "feishu:" + binding.BaseToken + ":" + binding.TableID, LinkedRecordIDs: ids, Transactions: []core.Transaction{}}
	if len(ids) == 0 {
		evidence.Issues = []core.EvidenceIssue{{Code: "missing_transaction_relation", Field: "transaction_relation"}}
		return evidence, nil
	}
	if len(ids) > 1 && !relation.Multiple {
		evidence.Issues = append(evidence.Issues, core.EvidenceIssue{Code: "multiple_transactions_in_single_relation", Field: "transaction_relation"})
	}
	schema, err := s.client.fieldSchema(ctx, token, binding.BaseToken, binding.TableID)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string)
	for _, semantic := range []string{"transaction_id", "original_amount", "original_currency", "merchant", "transaction_time", "booked_amount_cny", "country", "transaction_type", "transaction_status"} {
		if id := binding.Fields[semantic]; id != "" {
			field, ok := schema[id]
			if !ok {
				return nil, fmt.Errorf("transaction field %s does not exist", semantic)
			}
			if err := ValidateTransactionFieldType(semantic, field.Type); err != nil {
				return nil, err
			}
			names[semantic] = field.Name
		}
	}
	for _, id := range ids {
		fields, err := s.client.recordFields(ctx, token, binding.BaseToken, binding.TableID, id)
		if err != nil {
			return nil, fmt.Errorf("read linked transaction: %w", err)
		}
		transaction, issues := transactionFacts(id, fields, names)
		evidence.Transactions = append(evidence.Transactions, transaction)
		evidence.Issues = append(evidence.Issues, issues...)
	}
	return evidence, nil
}

func (c *LedgerClient) recordFields(ctx context.Context, token, base, table, recordID string) (map[string]json.RawMessage, error) {
	return c.recordFieldsQuery(ctx, token, base, table, recordID, "")
}

func (c *LedgerClient) recordFieldsQuery(ctx context.Context, token, base, table, recordID, query string) (map[string]json.RawMessage, error) {
	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records/%s", feishuAPI, url.PathEscape(base), url.PathEscape(table), url.PathEscape(recordID))
	if query != "" {
		endpoint += "?" + query
	}
	var data struct {
		Record struct {
			ID     string                     `json:"record_id"`
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"record"`
	}
	if err := c.request(ctx, http.MethodGet, endpoint, token, nil, &data); err != nil {
		return nil, err
	}
	if data.Record.ID != recordID || data.Record.Fields == nil {
		return nil, fmt.Errorf("Feishu record response does not match requested record")
	}
	return data.Record.Fields, nil
}

func relatedRecordIDs(raw json.RawMessage, tableID string) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("invalid transaction relation JSON")
	}
	seen := make(map[string]bool)
	var visit func(any) error
	visit = func(value any) error {
		switch cell := value.(type) {
		case nil:
			return nil
		case string:
			if cell == "" || cell != strings.TrimSpace(cell) {
				return fmt.Errorf("invalid related record ID")
			}
			seen[cell] = true
		case []any:
			for _, item := range cell {
				if err := visit(item); err != nil {
					return err
				}
			}
		case map[string]any:
			if target, exists := cell["table_id"]; exists && target != tableID {
				return fmt.Errorf("transaction relation points to another table")
			}
			for _, key := range []string{"record_ids", "link_record_ids", "record_id"} {
				if ids, exists := cell[key]; exists {
					return visit(ids)
				}
			}
			if text, ok := cell["text"].(string); ok && text != "" {
				return fmt.Errorf("transaction relation has display text but no record IDs")
			}
			if texts, ok := cell["text_arr"].([]any); ok && len(texts) > 0 {
				return fmt.Errorf("transaction relation has display text but no record IDs")
			}
			if _, hasTable := cell["table_id"]; !hasTable {
				return fmt.Errorf("unsupported transaction relation object")
			}
		default:
			return fmt.Errorf("unsupported transaction relation value")
		}
		return nil
	}
	if err := visit(value); err != nil {
		return nil, err
	}
	if len(seen) > 100 {
		return nil, fmt.Errorf("transaction relation exceeds 100 records")
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func transactionFacts(recordID string, fields map[string]json.RawMessage, names map[string]string) (core.Transaction, []core.EvidenceIssue) {
	var issues []core.EvidenceIssue
	text := func(semantic string) string { return fieldText(fields, names[semantic]) }
	issue := func(code, semantic, raw string) {
		issues = append(issues, core.EvidenceIssue{Code: code, RecordID: recordID, Field: semantic, RawValue: raw})
	}
	required := func(semantic string) string {
		value := text(semantic)
		if value == "" {
			issue("missing_transaction_field", semantic, "")
		}
		return value
	}
	amount := func(semantic string, mandatory bool) string {
		value := text(semantic)
		if value == "" {
			if mandatory {
				issue("missing_transaction_field", semantic, "")
			}
			return ""
		}
		number, ok := invoice.Decimal(value)
		if !ok {
			issue("invalid_transaction_amount", semantic, value)
			return ""
		}
		return number.String()
	}
	transaction := core.Transaction{RecordID: recordID, TransactionID: required("transaction_id"), Merchant: required("merchant"),
		OriginalAmount: amount("original_amount", true), OriginalCurrency: strings.ToUpper(required("original_currency")),
		BookedAmountCNY: amount("booked_amount_cny", false), Country: text("country"), TransactionType: text("transaction_type"), Status: text("transaction_status")}
	if currency := transaction.OriginalCurrency; currency != "" {
		valid := len(currency) == 3
		for _, char := range currency {
			if char < 'A' || char > 'Z' {
				valid = false
			}
		}
		if !valid {
			issue("invalid_transaction_currency", "original_currency", text("original_currency"))
			transaction.OriginalCurrency = ""
		}
	}
	if value := required("transaction_time"); value != "" {
		millis, err := strconv.ParseInt(value, 10, 64)
		if err != nil || time.UnixMilli(millis).Year() < 1 || time.UnixMilli(millis).Year() > 9999 {
			issue("invalid_transaction_time", "transaction_time", value)
		} else {
			transaction.OccurredAt = time.UnixMilli(millis).UTC().Format(time.RFC3339Nano)
		}
	}
	return transaction, issues
}

func ValidateTransactionFieldType(semantic string, fieldType int) error {
	var allowed []int
	switch semantic {
	case "transaction_id", "merchant":
		allowed = []int{1}
	case "original_amount", "booked_amount_cny":
		allowed = []int{1, 2}
	case "original_currency", "country", "transaction_type", "transaction_status":
		allowed = []int{1, 3}
	case "transaction_time":
		allowed = []int{5}
	default:
		return nil
	}
	for _, value := range allowed {
		if fieldType == value {
			return nil
		}
	}
	return fmt.Errorf("transaction field %s has incompatible type %d", semantic, fieldType)
}
