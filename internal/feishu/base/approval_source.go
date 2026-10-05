package base

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

// Bindings use physical field IDs. Only this adapter understands Base cell types.
type ApprovalInputBinding struct {
	Role, FieldID, Kind, Currency, CurrencyFieldID string
	PersonMap                                      map[string]string
}
type ApprovalGroupBinding struct{ Axis, Role, FieldID, Period, Timezone string }
type ApprovalSourceBinding struct {
	BaseToken, DetailTableID, TransactionTableID, TransactionRelationFieldID, TargetScope string
	Inputs                                                                                map[string]ApprovalInputBinding
	Groups                                                                                []ApprovalGroupBinding
}
type ApprovalFieldsSource struct {
	client         *LedgerClient
	binding        ApprovalSourceBinding
	scope, version string
	transactions   bool
}

func NewApprovalFieldsSource(client *LedgerClient, binding ApprovalSourceBinding) (*ApprovalFieldsSource, error) {
	if client == nil || client.appID == "" || client.appSecret == "" || binding.BaseToken == "" || binding.DetailTableID == "" || !strings.HasPrefix(binding.TargetScope, "feishu-app:") || len(binding.TargetScope) <= len("feishu-app:") || len(binding.Inputs) == 0 {
		return nil, fmt.Errorf("approval source requires explicit app identity, physical source and target bindings")
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	var frozen ApprovalSourceBinding
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return nil, err
	}
	s := &ApprovalFieldsSource{client: client, binding: frozen, scope: "feishu:" + binding.BaseToken + ":" + binding.DetailTableID}
	role := func(role string) bool {
		if role == "transactions" {
			s.transactions = true
			return true
		}
		return role == "reimbursement_details"
	}
	for semantic, input := range frozen.Inputs {
		if semantic == "" || semantic != strings.TrimSpace(semantic) {
			return nil, fmt.Errorf("approval inputs require stable semantic keys")
		}
		if input.Role == "review" {
			if input.Kind != "text" || (input.FieldID != "decision" && input.FieldID != "comment") {
				return nil, fmt.Errorf("approval review fields require saved decision/comment")
			}
		} else if !role(input.Role) || input.FieldID == "" {
			return nil, fmt.Errorf("approval inputs require declared physical fields")
		}
		switch input.Kind {
		case "text", "date", "number", "money", "people", "files":
		default:
			return nil, fmt.Errorf("approval source kind is unsupported")
		}
		if input.Kind == "money" {
			if (input.Currency == "") == (input.CurrencyFieldID == "") || (input.Currency != "" && !approvalCurrency(input.Currency)) {
				return nil, fmt.Errorf("approval money requires one explicit currency binding")
			}
		} else if input.Currency != "" || input.CurrencyFieldID != "" {
			return nil, fmt.Errorf("approval currency belongs only to money")
		}
		if len(input.PersonMap) > 0 && input.Kind != "people" {
			return nil, fmt.Errorf("approval person mapping belongs only to people")
		}
		for source, target := range input.PersonMap {
			if !approvalPersonID(source) || !approvalPersonID(target) {
				return nil, fmt.Errorf("approval person map requires app open IDs")
			}
		}
	}
	seen := map[string]bool{}
	for _, group := range frozen.Groups {
		if group.Axis == "" || seen[group.Axis] || !role(group.Role) || group.FieldID == "" {
			return nil, fmt.Errorf("approval groups require unique axes and physical fields")
		}
		seen[group.Axis] = true
		if group.Period != "" {
			if group.Period != "day" && group.Period != "month" {
				return nil, fmt.Errorf("approval group period is unsupported")
			}
			if group.Timezone == "" {
				return nil, fmt.Errorf("approval group requires an explicit timezone")
			}
			if _, err := time.LoadLocation(group.Timezone); err != nil {
				return nil, fmt.Errorf("approval group timezone is invalid")
			}
		} else if group.Timezone != "" {
			return nil, fmt.Errorf("approval timezone requires date bucketing")
		}
	}
	if s.transactions && (binding.TransactionTableID == "" || binding.TransactionTableID == binding.DetailTableID || binding.TransactionRelationFieldID == "") {
		return nil, fmt.Errorf("approval transaction inputs require a distinct linked table")
	}
	// Group declaration order is not a business change; policy/value edits are.
	sort.Slice(s.binding.Groups, func(i, j int) bool { return s.binding.Groups[i].Axis < s.binding.Groups[j].Axis })
	encoded, _ = json.Marshal(struct {
		Identity string
		Binding  ApprovalSourceBinding
	}{"feishu-app:" + client.appID, s.binding})
	hash := sha256.Sum256(encoded)
	s.version = hex.EncodeToString(hash[:])
	return s, nil
}

func (s *ApprovalFieldsSource) ApprovalSourceScope() string { return s.scope }

// Reads schema once per table and follows a single native payment relation when
// requested. Multiple payments require an explicit allocation policy, never first/sum.
// No downloads, uploads, writes or instance calls exist in this adapter.
func (s *ApprovalFieldsSource) ReadApprovalInputs(ctx context.Context, ids []string) ([]app.InputCheck, error) {
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || id != strings.TrimSpace(id) || seen[id] {
			return nil, fmt.Errorf("approval inputs require unique explicit record IDs")
		}
		seen[id] = true
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("approval inputs require selected records")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token, err := s.client.accessToken(ctx)
	if err != nil {
		return nil, approvalReadError(ctx, "approval source authentication unavailable")
	}
	details, err := s.client.fieldSchema(ctx, token, s.binding.BaseToken, s.binding.DetailTableID)
	if err != nil {
		return nil, approvalReadError(ctx, "approval detail schema unavailable")
	}
	schemas := map[string]map[string]ledgerField{"reimbursement_details": details}
	if s.transactions {
		transactions, err := s.client.fieldSchema(ctx, token, s.binding.BaseToken, s.binding.TransactionTableID)
		if err != nil {
			return nil, approvalReadError(ctx, "approval transaction schema unavailable")
		}
		schemas["transactions"] = transactions
	}
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	checks := make([]app.InputCheck, 0, len(ids))
	payments := map[string]map[string]json.RawMessage{}
	for _, id := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		check := app.InputCheck{RecordID: id, Issues: []app.InputIssue{}, ReviewFields: map[string]string{}, Row: core.Row{SourceScope: s.scope, SourceVersion: s.version, RecordID: id, Fields: map[string]core.Value{}, GroupValues: map[string]string{}}}
		issue := func(input, code string) {
			check.Issues = append(check.Issues, app.InputIssue{Input: input, Code: code})
		}
		fields, err := s.client.recordFieldsQuery(ctx, token, s.binding.BaseToken, s.binding.DetailTableID, id, "user_id_type=open_id&text_field_as_array=true")
		if err != nil {
			issue("source", "source_unavailable")
			checks = append(checks, check)
			continue
		}
		values := map[string]map[string]json.RawMessage{"reimbursement_details": fields}
		txIssue := ""
		if s.transactions {
			relation, exists := details[s.binding.TransactionRelationFieldID]
			if !exists || (relation.Type != 18 && relation.Type != 21) || relation.RelatedTableID != s.binding.TransactionTableID {
				txIssue = "transaction_relation_invalid"
			} else {
				linked, err := relatedRecordIDs(fields[relation.Name], s.binding.TransactionTableID)
				if err != nil {
					txIssue = "transaction_relation_invalid"
				} else if len(linked) == 0 {
					txIssue = "transaction_missing"
				} else if len(linked) != 1 {
					txIssue = "transaction_ambiguous"
				} else {
					payment, cached := payments[linked[0]]
					if !cached {
						payment, err = s.client.recordFieldsQuery(ctx, token, s.binding.BaseToken, s.binding.TransactionTableID, linked[0], "user_id_type=open_id&text_field_as_array=true")
						if err == nil {
							payments[linked[0]] = payment
						}
					}
					if err != nil {
						txIssue = "transaction_unavailable"
					} else {
						values["transactions"] = payment
					}
				}
			}
		}
		inputNames := make([]string, 0, len(s.binding.Inputs))
		for semantic := range s.binding.Inputs {
			inputNames = append(inputNames, semantic)
		}
		sort.Strings(inputNames)
		for _, semantic := range inputNames {
			input := s.binding.Inputs[semantic]
			if input.Role == "review" {
				check.ReviewFields[semantic] = input.FieldID
				continue
			}
			if input.Role == "transactions" && txIssue != "" {
				issue(semantic, txIssue)
				continue
			}
			value, code := s.inputValue(input, schemas[input.Role], values[input.Role])
			if code != "" {
				issue(semantic, code)
			} else {
				check.Row.Fields[semantic] = value
			}
		}
		for _, group := range s.binding.Groups {
			if group.Role == "transactions" && txIssue != "" {
				issue("group:"+group.Axis, txIssue)
				continue
			}
			value, code := s.groupValue(group, schemas[group.Role], values[group.Role])
			if code != "" {
				issue("group:"+group.Axis, code)
			} else {
				check.Row.GroupValues[group.Axis] = value
			}
		}
		checks = append(checks, check)
	}
	return checks, ctx.Err()
}

func approvalReadError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}
