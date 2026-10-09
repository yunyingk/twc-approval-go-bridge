package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
)

type schemaInspector interface {
	InspectFields(context.Context, string, string) (map[string]base.TableField, error)
}

type checkedTable struct {
	Role          string `json:"role"`
	Name          string `json:"name"`
	BaseToken     string `json:"base_token"`
	TableID       string `json:"table_id"`
	Source        string `json:"source"`
	Access        string `json:"access"`
	CheckedFields int    `json:"checked_fields"`
}

func runBusinessCheck(ctx context.Context, cfg config.Config, output io.Writer) error {
	if cfg.Business == nil || !cfg.FeishuEnabled() {
		return fmt.Errorf("check-business-config requires a complete configuration and Feishu app credentials")
	}
	return checkBusiness(ctx, cfg.Business, base.NewLedgerClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret), output)
}

func checkBusiness(ctx context.Context, profile *config.BusinessProfile, inspector schemaInspector, output io.Writer) error {
	roles := []struct {
		role  string
		table config.TableBinding
	}{
		{"transactions", profile.Tables.Transactions},
		{"reimbursement_details", profile.Tables.ReimbursementDetails},
		{"invoice_ledger", profile.Tables.InvoiceLedger},
	}
	checked := make([]checkedTable, 0, len(roles))
	for _, role := range roles {
		table := role.table
		schema, err := inspector.InspectFields(ctx, table.BaseToken, table.TableID)
		if err != nil {
			return fmt.Errorf("check %s: %w", role.role, err)
		}
		fields := make(map[string]string)
		for semantic, id := range table.Fields {
			fields[semantic] = id
		}
		if role.role == "reimbursement_details" {
			for semantic, id := range profile.Review.ContextFields {
				fields["context."+semantic] = id
			}
			for semantic, id := range profile.Review.ResultFields {
				fields["result."+semantic] = id
			}
		}
		semantics := make([]string, 0, len(fields))
		for semantic := range fields {
			semantics = append(semantics, semantic)
		}
		sort.Strings(semantics)
		for _, semantic := range semantics {
			field, ok := schema[fields[semantic]]
			if !ok {
				return fmt.Errorf("%s.%s field %s does not exist", role.role, semantic, fields[semantic])
			}
			if err := checkFieldType(role.role, semantic, field); err != nil {
				return err
			}
		}
		// Reusing IDs from a copied Base can leave valid-looking links aimed at
		// an old table. Check the native relationships used by this workflow.
		links := map[string]string{}
		switch role.role {
		case "reimbursement_details":
			if table.BaseToken == profile.Tables.Transactions.BaseToken {
				links["transaction_relation"] = profile.Tables.Transactions.TableID
			}
			links["invoice_relation"] = profile.Tables.InvoiceLedger.TableID
		case "invoice_ledger":
			links["relation"] = profile.Tables.ReimbursementDetails.TableID
			links["feishu_detail_relation"] = profile.Tables.ReimbursementDetails.TableID
		case "transactions":
			if table.BaseToken == profile.Tables.ReimbursementDetails.BaseToken {
				links["detail_relation"] = profile.Tables.ReimbursementDetails.TableID
			}
		}
		for semantic, target := range links {
			if id := fields[semantic]; id != "" {
				field := schema[id]
				if (field.Type != 18 && field.Type != 21) || field.RelatedTableID != target {
					return fmt.Errorf("%s.%s must link to configured table %s", role.role, semantic, target)
				}
			}
		}
		checked = append(checked, checkedTable{Role: role.role, Name: table.Name, BaseToken: table.BaseToken, TableID: table.TableID, Source: table.Source, Access: table.Access, CheckedFields: len(fields)})
	}
	return json.NewEncoder(output).Encode(struct {
		Profile string         `json:"profile"`
		Tables  []checkedTable `json:"tables"`
	}{profile.Name, checked})
}

func checkFieldType(role, semantic string, field base.TableField) error {
	var types []int
	switch role {
	case "transactions":
		return base.ValidateTransactionFieldType(semantic, field.Type)
	case "reimbursement_details":
		switch {
		case semantic == "attachment":
			types = []int{17}
		case semantic == "detail_id":
			types = []int{1, 1005}
		case len(semantic) > 7 && semantic[:7] == "result.":
			types = []int{1}
		}
	case "invoice_ledger":
		norm := semantic
		for _, p := range []string{"ocr_", "bridge_", "feishu_"} {
			norm = strings.TrimPrefix(norm, p)
		}
		switch norm {
		case "relation", "detail_relation":
			types = []int{18, 21}
		case "origin_attachment":
			types = []int{17, 19}
		case "pretax_amount", "tax_amount", "tax_rate", "total_amount":
			types = []int{1, 2}
		case "issue_date":
			types = []int{5}
		case "recognition_status":
			types = []int{1, 3}
		default:
			types = []int{1}
		}
	}
	if len(types) == 0 {
		return nil
	}
	for _, allowed := range types {
		if field.Type == allowed {
			return nil
		}
	}
	return fmt.Errorf("%s.%s has incompatible field type %d; expected %v", role, semantic, field.Type, types)
}
