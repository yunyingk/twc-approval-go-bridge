package config

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Configured grouping zones also work in static runtime images.

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

// ApprovalSettings is an explicit human-approval policy, separate from AI
// provider selection. It contains bindings and actor IDs, never credentials.
type ApprovalSettings struct {
	Mode               string                      `json:"mode"`                      // disabled or manual; no automatic creation yet
	TargetIdentity     string                      `json:"target_identity,omitempty"` // bridge or approval
	TemplateCode       string                      `json:"template_code,omitempty"`
	SubmitterOpenID    string                      `json:"submitter_open_id,omitempty"`
	DepartmentID       string                      `json:"department_id,omitempty"`
	AllowedAIDecisions []string                    `json:"allowed_ai_decisions,omitempty"`
	GroupBy            []ApprovalGroup             `json:"group_by"`
	InputFields        map[string]ApprovalInput    `json:"input_fields,omitempty"`
	DetailControl      ApprovalSelector            `json:"detail_control,omitempty"`
	FormFields         map[string]ApprovalSelector `json:"form_fields,omitempty"`
	NodeApprovers      map[string][]string         `json:"node_approvers,omitempty"`
}
type ApprovalSelector struct {
	ID       string `json:"id,omitempty"`
	CustomID string `json:"custom_id,omitempty"`
}
type ApprovalGroup struct {
	Axis     string `json:"axis"`
	Role     string `json:"role"`
	Field    string `json:"field"`            // stable semantic key in the selected table role
	Period   string `json:"period,omitempty"` // day/month; omitted means exact value
	Timezone string `json:"timezone,omitempty"`
}
type ApprovalInput struct {
	Role          string            `json:"role"` // reimbursement_details, transactions or review
	Field         string            `json:"field"`
	Kind          string            `json:"kind"` // text/date/number/money/people/files
	Currency      string            `json:"currency,omitempty"`
	CurrencyField string            `json:"currency_field,omitempty"`
	PersonMap     map[string]string `json:"person_map,omitempty"` // source open ID -> target app open ID
}

func explicit(value string) bool { return value != "" && value == strings.TrimSpace(value) }
func approvalOpenID(value string) bool {
	return explicit(value) && strings.HasPrefix(value, "ou_") && len(value) > 3
}
func selectorValid(s ApprovalSelector) bool {
	return (explicit(s.ID) && s.CustomID == "") || (explicit(s.CustomID) && s.ID == "")
}
func approvalRole(p *BusinessProfile, role string) (TableBinding, bool) {
	switch role {
	case "reimbursement_details":
		return p.Tables.ReimbursementDetails, true
	case "transactions":
		return p.Tables.Transactions, true
	}
	return TableBinding{}, false
}
func (a *ApprovalSettings) validate(p *BusinessProfile) error {
	if a.Mode == "disabled" {
		return nil
	} // An inactive draft may be incomplete.
	if a.Mode != "manual" {
		return fmt.Errorf("approval.mode must be disabled or manual")
	}
	if (a.TargetIdentity != "bridge" && a.TargetIdentity != "approval") || !explicit(a.TemplateCode) || !approvalOpenID(a.SubmitterOpenID) || a.DepartmentID != strings.TrimSpace(a.DepartmentID) {
		return fmt.Errorf("manual approval requires explicit target_identity, template_code and target application submitter_open_id")
	}
	if len(a.AllowedAIDecisions) == 0 {
		return fmt.Errorf("approval requires explicit allowed_ai_decisions")
	}
	seen := map[string]bool{}
	for _, decision := range a.AllowedAIDecisions {
		if (core.Outcome{Decision: decision}).Validate() != nil || seen[decision] {
			return fmt.Errorf("approval allowed_ai_decisions are invalid or duplicated")
		}
		seen[decision] = true
	}
	seen = map[string]bool{}
	if a.GroupBy == nil {
		return fmt.Errorf("approval requires explicit group_by; an empty array selects one batch")
	}
	for _, group := range a.GroupBy {
		table, ok := approvalRole(p, group.Role)
		if !explicit(group.Axis) || seen[group.Axis] || !ok || table.Fields[group.Field] == "" {
			return fmt.Errorf("approval grouping requires unique axes and declared physical source fields")
		}
		seen[group.Axis] = true
		if group.Role == "transactions" && (!p.Review.IncludeTransactions || table.BaseToken != p.Tables.ReimbursementDetails.BaseToken) {
			return fmt.Errorf("approval transaction grouping requires linked transactions in the same Base")
		}
		if group.Period == "" {
			if group.Timezone != "" {
				return fmt.Errorf("approval timezone requires a day/month period")
			}
		} else if group.Period != "day" && group.Period != "month" {
			return fmt.Errorf("approval period must be day or month")
		} else if !explicit(group.Timezone) {
			return fmt.Errorf("approval period requires an explicit timezone")
		} else if _, err := time.LoadLocation(group.Timezone); err != nil {
			return fmt.Errorf("approval timezone is invalid")
		}
	}
	if !selectorValid(a.DetailControl) || len(a.InputFields) == 0 || len(a.FormFields) != len(a.InputFields) {
		return fmt.Errorf("approval requires a detail control and complete input/form field bindings")
	}
	used := map[string]bool{}
	for semantic, input := range a.InputFields {
		selector, bound := a.FormFields[semantic]
		key := selector.ID + "\x00" + selector.CustomID
		if !explicit(semantic) || !bound || !selectorValid(selector) || used[key] {
			return fmt.Errorf("approval form bindings must be explicit, complete and unique")
		}
		used[key] = true
		table, physical := approvalRole(p, input.Role)
		if input.Role == "review" {
			if input.Kind != "text" || (input.Field != "decision" && input.Field != "comment") {
				return fmt.Errorf("approval review input supports only the saved current decision/comment as text")
			}
		} else if !physical || table.Fields[input.Field] == "" {
			return fmt.Errorf("approval input must reference a declared source field")
		}
		switch input.Kind {
		case "text", "date", "number", "money", "people", "files":
		default:
			return fmt.Errorf("unsupported approval input kind")
		}
		if input.Kind == "money" {
			if (input.Currency == "") == (input.CurrencyField == "") ||
				(input.Currency != "" && (!explicit(input.Currency) || len(input.Currency) != 3 || strings.Trim(input.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "")) ||
				(input.CurrencyField != "" && (!physical || table.Fields[input.CurrencyField] == "")) {
				return fmt.Errorf("approval money requires one explicit currency or declared same-source currency_field")
			}
		} else if input.Currency != "" || input.CurrencyField != "" {
			return fmt.Errorf("approval currency belongs only to money inputs")
		}
		if len(input.PersonMap) > 0 && input.Kind != "people" {
			return fmt.Errorf("approval person_map belongs only to people inputs")
		}
		for source, target := range input.PersonMap {
			if !approvalOpenID(source) || !approvalOpenID(target) {
				return fmt.Errorf("approval person_map requires source and target application open IDs")
			}
		}
		if input.Kind == "people" && a.TargetIdentity == "approval" && len(input.PersonMap) == 0 {
			return fmt.Errorf("separate approval identity requires explicit people mappings")
		}
		if input.Role == "transactions" && (!p.Review.IncludeTransactions || table.BaseToken != p.Tables.ReimbursementDetails.BaseToken) {
			return fmt.Errorf("approval transaction inputs require linked transactions in the same Base")
		}
	}
	if len(a.NodeApprovers) > 20 {
		return fmt.Errorf("approval supports at most 20 selected nodes")
	}
	for node, ids := range a.NodeApprovers {
		if !explicit(node) || len(ids) == 0 {
			return fmt.Errorf("approval selected node requires ID and people")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !approvalOpenID(id) || seen[id] {
				return fmt.Errorf("approval node people must be unique target application open IDs")
			}
			seen[id] = true
		}
	}
	return nil
}

// ApprovalCredentials never falls back to a different application identity.
func (c Config) ApprovalCredentials() (string, string, error) {
	if c.Business == nil || c.Business.Approval == nil || c.Business.Approval.Mode != "manual" {
		return "", "", fmt.Errorf("manual approval configuration is not selected")
	}
	if c.Business.Approval.TargetIdentity == "bridge" {
		if c.FeishuAppID == "" || c.FeishuAppSecret == "" {
			return "", "", fmt.Errorf("selected bridge approval identity requires FEISHU_APP_ID/SECRET")
		}
		return c.FeishuAppID, c.FeishuAppSecret, nil
	}
	if c.Business.Approval.TargetIdentity == "approval" {
		if c.FeishuApprovalAppID == "" || c.FeishuApprovalAppSecret == "" {
			return "", "", fmt.Errorf("selected separate approval identity requires FEISHU_APPROVAL_APP_ID/SECRET")
		}
		return c.FeishuApprovalAppID, c.FeishuApprovalAppSecret, nil
	}
	return "", "", fmt.Errorf("unknown approval target identity")
}

// SavedApprovalCredentials resolves only the exact application pinned by an old
// plan. It does not require current template/business configuration or fall back
// to a newly selected application when the original credentials are unavailable.
func (c Config) SavedApprovalCredentials(scope string) (string, string, error) {
	var id, secret string
	matches := 0
	for _, pair := range [][2]string{{c.FeishuAppID, c.FeishuAppSecret}, {c.FeishuApprovalAppID, c.FeishuApprovalAppSecret}} {
		if pair[0] != "" && scope == "feishu-app:"+pair[0] {
			id, secret, matches = pair[0], pair[1], matches+1
		}
	}
	if matches != 1 || secret == "" {
		return "", "", fmt.Errorf("saved approval target requires one unambiguous original application credential group")
	}
	return id, secret, nil
}
