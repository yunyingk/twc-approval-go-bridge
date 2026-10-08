package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// BusinessProfile binds business roles to physical tables inside the configuration.
// A process selects one complete profile; display names never identify task state.
type BusinessProfile struct {
	Version     int                 `json:"version" toml:"version"`
	Name        string              `json:"name" toml:"name"`
	Tables      BusinessTables      `json:"tables" toml:"tables"`
	Recognition RecognitionSettings `json:"recognition" toml:"recognition"`
	Review      ReviewSettings      `json:"review" toml:"review"`
}

type BusinessTables struct {
	Transactions         TableBinding `json:"transactions" toml:"transactions"`
	ReimbursementDetails TableBinding `json:"reimbursement_details" toml:"reimbursement_details"`
	InvoiceLedger        TableBinding `json:"invoice_ledger" toml:"invoice_ledger"`
}

// TablesProfile defines the schema layout of business tables decoupled from runtime secrets.
type TablesProfile struct {
	Version int          `json:"version"`
	Name    string       `json:"name"`
	Tables  TablesSchema `json:"tables"`
}

type TablesSchema struct {
	Transactions         TableBinding                `json:"transactions"`
	ReimbursementDetails ReimbursementDetailsBinding `json:"reimbursement_details"`
	InvoiceLedger        TableBinding                `json:"invoice_ledger"`
}

// ReimbursementDetailsBinding binds the reimbursement details table, including
// regular fields, review context fields and AI review result write-back fields.
type ReimbursementDetailsBinding struct {
	Name          string            `json:"name"`
	Source        string            `json:"source"`
	Access        string            `json:"access"`
	BaseToken     string            `json:"base_token"`
	TableID       string            `json:"table_id"`
	Fields        map[string]string `json:"fields"`
	ContextFields map[string]string `json:"context_fields,omitempty"`
	ResultFields  map[string]string `json:"result_fields,omitempty"`
}

func (r ReimbursementDetailsBinding) TableBinding() TableBinding {
	return TableBinding{
		Name:      r.Name,
		Source:    r.Source,
		Access:    r.Access,
		BaseToken: r.BaseToken,
		TableID:   r.TableID,
		Fields:    r.Fields,
	}
}

// Source describes who supplies records, not an HTTP endpoint or an authentication grant.
// Access is the bridge's declared use; actual Feishu permissions still apply.
type TableBinding struct {
	Name      string            `json:"name" toml:"name"`
	Source    string            `json:"source" toml:"source"`
	Access    string            `json:"access" toml:"access"`
	BaseToken string            `json:"base_token" toml:"base_token"`
	TableID   string            `json:"table_id" toml:"table_id"`
	Fields    map[string]string `json:"fields" toml:"fields"`
}

type RecognitionSettings struct {
	Provider     string `json:"provider" toml:"provider"`
	TriggerMode  string `json:"trigger_mode" toml:"trigger_mode"`
	PollInterval string `json:"poll_interval,omitempty" toml:"poll_interval,omitempty"`
	PollStartup  string `json:"poll_startup,omitempty" toml:"poll_startup,omitempty"`
}

// ContextFields and ResultFields both belong to the reimbursement-details table.
// The optional rules file is read only when the model reviewer is constructed.
type ReviewSettings struct {
	RulesFile              string            `json:"rules_file,omitempty" toml:"rules_file,omitempty"`
	ResubmitOnDetailChange bool              `json:"resubmit_on_detail_change,omitempty" toml:"resubmit_on_detail_change,omitempty"`
	ResubmitOnSourceChange bool              `json:"resubmit_on_source_change,omitempty" toml:"resubmit_on_source_change,omitempty"`
	ChangeDebounce         string            `json:"change_debounce,omitempty" toml:"change_debounce,omitempty"`
	IncludeTransactions    bool              `json:"include_transactions,omitempty" toml:"include_transactions,omitempty"`
	Provider               string            `json:"provider" toml:"provider"`
	TriggerMode            string            `json:"trigger_mode" toml:"trigger_mode"`
	ContextFields          map[string]string `json:"context_fields,omitempty" toml:"-"`
	ResultFields           map[string]string `json:"result_fields,omitempty" toml:"-"`
}

// LoadTablesFile loads and validates a business tables schema from a JSON file.
func LoadTablesFile(path string) (TablesProfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return TablesProfile{}, fmt.Errorf("open tables file %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return TablesProfile{}, fmt.Errorf("tables file must be a regular file of at most 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var profile TablesProfile
	if err := decoder.Decode(&profile); err != nil {
		return TablesProfile{}, fmt.Errorf("invalid tables JSON: %w", err)
	}
	if err := profile.validate(); err != nil {
		return TablesProfile{}, err
	}
	return profile, nil
}

func (tp *TablesProfile) validate() error {
	if tp.Version != 1 || strings.TrimSpace(tp.Name) == "" {
		return fmt.Errorf("version must be 1 and name must be set")
	}
	roles := []struct {
		role, access string
		table        TableBinding
	}{
		{"transactions", "read_only", tp.Tables.Transactions},
		{"reimbursement_details", "read_write", tp.Tables.ReimbursementDetails.TableBinding()},
		{"invoice_ledger", "read_write", tp.Tables.InvoiceLedger},
	}
	usedTables := make(map[string]bool)
	for _, binding := range roles {
		table := binding.table
		if strings.TrimSpace(table.Name) == "" || strings.TrimSpace(table.Source) == "" ||
			strings.TrimSpace(table.BaseToken) == "" || strings.TrimSpace(table.TableID) == "" {
			return fmt.Errorf("tables.%s requires name, source, base_token and table_id", binding.role)
		}
		if table.BaseToken != strings.TrimSpace(table.BaseToken) || table.TableID != strings.TrimSpace(table.TableID) {
			return fmt.Errorf("tables.%s IDs must not contain surrounding whitespace", binding.role)
		}
		if table.Access != binding.access {
			return fmt.Errorf("tables.%s access must be %s", binding.role, binding.access)
		}
		key := table.BaseToken + ":" + table.TableID
		if usedTables[key] {
			return fmt.Errorf("business roles must refer to distinct tables")
		}
		usedTables[key] = true
		if err := validateFieldMapping("tables."+binding.role+".fields", table.Fields); err != nil {
			return err
		}
	}
	if tp.Tables.ReimbursementDetails.BaseToken != tp.Tables.InvoiceLedger.BaseToken {
		// The current ledger adapter and native relationship use the source Base.
		// Reject an unsupported split before any worker or external write starts.
		return fmt.Errorf("reimbursement_details and invoice_ledger must currently use the same Base; cross-Base ledger delivery is not implemented")
	}
	if tp.Tables.Transactions.Fields["transaction_id"] == "" ||
		tp.Tables.ReimbursementDetails.Fields["attachment"] == "" ||
		tp.Tables.InvoiceLedger.Fields["source_key"] == "" || tp.Tables.InvoiceLedger.Fields["raw_json"] == "" {
		return fmt.Errorf("transaction_id, attachment and ledger source_key/raw_json bindings are required")
	}
	if err := validateFieldMapping("review.context_fields", tp.Tables.ReimbursementDetails.ContextFields); err != nil {
		return err
	}
	for semantic := range tp.Tables.ReimbursementDetails.ContextFields {
		if semantic == "bridge_revision" || strings.HasPrefix(semantic, "bridge_transaction_") {
			return fmt.Errorf("review context uses reserved bridge evidence key")
		}
	}
	if err := validateFieldMapping("review.result_fields", tp.Tables.ReimbursementDetails.ResultFields); err != nil {
		return err
	}
	// Result fields must never overwrite any declared employee input, including
	// the transaction relationship. Check all source bindings, not just attachments.
	for _, id := range tp.Tables.ReimbursementDetails.ResultFields {
		for _, sourceID := range tp.Tables.ReimbursementDetails.Fields {
			if id == sourceID {
				return fmt.Errorf("review result fields must not overwrite reimbursement input fields")
			}
		}
	}
	return nil
}

func (p *BusinessProfile) validate() error {
	tp := TablesProfile{
		Version: p.Version,
		Name:    p.Name,
		Tables: TablesSchema{
			Transactions: p.Tables.Transactions,
			ReimbursementDetails: ReimbursementDetailsBinding{
				Name:          p.Tables.ReimbursementDetails.Name,
				Source:        p.Tables.ReimbursementDetails.Source,
				Access:        p.Tables.ReimbursementDetails.Access,
				BaseToken:     p.Tables.ReimbursementDetails.BaseToken,
				TableID:       p.Tables.ReimbursementDetails.TableID,
				Fields:        p.Tables.ReimbursementDetails.Fields,
				ContextFields: p.Review.ContextFields,
				ResultFields:  p.Review.ResultFields,
			},
			InvoiceLedger: p.Tables.InvoiceLedger,
		},
	}
	if err := tp.validate(); err != nil {
		return err
	}
	if p.Review.IncludeTransactions {
		if p.Tables.Transactions.BaseToken != p.Tables.ReimbursementDetails.BaseToken {
			return fmt.Errorf("linked transaction review currently requires transactions and reimbursement_details in the same Base")
		}
		if p.Tables.ReimbursementDetails.Fields["transaction_relation"] == "" {
			return fmt.Errorf("linked transaction review requires reimbursement_details.transaction_relation")
		}
		for _, semantic := range []string{"transaction_id", "original_amount", "original_currency", "merchant", "transaction_time"} {
			if p.Tables.Transactions.Fields[semantic] == "" {
				return fmt.Errorf("linked transaction review requires transactions.%s", semantic)
			}
		}
	}
	switch p.Recognition.Provider {
	case "disabled", "anyreceipt", "model":
	default:
		return fmt.Errorf("recognition.provider must be disabled, anyreceipt or model")
	}
	switch p.Recognition.TriggerMode {
	case "event", "poll", "both":
	default:
		return fmt.Errorf("recognition.trigger_mode must be event, poll or both")
	}
	if p.Review.Provider != "seal" && p.Review.Provider != "model" {
		return fmt.Errorf("review.provider must be seal or model")
	}
	if p.Review.TriggerMode != "manual" && p.Review.TriggerMode != "after_recognition" {
		return fmt.Errorf("review.trigger_mode must be manual or after_recognition")
	}
	if p.Review.ResubmitOnDetailChange && p.Review.TriggerMode != "after_recognition" {
		return fmt.Errorf("resubmit_on_detail_change requires after_recognition")
	}
	if p.Review.ResubmitOnSourceChange {
		if p.Review.TriggerMode != "after_recognition" {
			return fmt.Errorf("resubmit_on_source_change requires after_recognition")
		}
		if p.Tables.InvoiceLedger.Fields["invoice_number"] == "" {
			return fmt.Errorf("source change review requires invoice_ledger.invoice_number")
		}
	}
	if p.Review.ChangeDebounce != "" {
		wait, err := time.ParseDuration(p.Review.ChangeDebounce)
		if err != nil || wait < time.Second || wait > 10*time.Minute {
			return fmt.Errorf("review.change_debounce must be between 1s and 10m")
		}
	}
	return nil
}

func (p *BusinessProfile) apply(cfg *Config) {
	detail, ledger := p.Tables.ReimbursementDetails, p.Tables.InvoiceLedger
	cfg.ReceiptBaseToken, cfg.ReceiptTableID = detail.BaseToken, detail.TableID
	cfg.ReceiptFieldID, cfg.ReceiptSourceDetailFieldID = detail.Fields["attachment"], detail.Fields["detail_id"]
	cfg.ReceiptLedgerTableID, cfg.ReceiptLedgerFieldIDs = ledger.TableID, ledger.Fields
	cfg.ReceiptProvider, cfg.ReceiptTriggerMode = p.Recognition.Provider, p.Recognition.TriggerMode
	if cfg.ReceiptProvider == "disabled" {
		cfg.ReceiptProvider = ""
	}
	cfg.ReviewProvider, cfg.ReviewTriggerMode = p.Review.Provider, p.Review.TriggerMode
	cfg.ReviewContextFieldIDs, cfg.ReviewResultFieldIDs = p.Review.ContextFields, p.Review.ResultFields
}

func validateFieldMapping(label string, fields map[string]string) error {
	used := make(map[string]bool)
	for semantic, id := range fields {
		if strings.TrimSpace(semantic) == "" || strings.TrimSpace(id) == "" ||
			semantic != strings.TrimSpace(semantic) || id != strings.TrimSpace(id) || used[id] {
			return fmt.Errorf("%s: empty, whitespace or duplicate field mapping", label)
		}
		used[id] = true
	}
	return nil
}
