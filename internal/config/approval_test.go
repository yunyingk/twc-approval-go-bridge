package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func approvalProfile() BusinessProfile {
	p := testProfile()
	p.Tables.ReimbursementDetails.Fields["employee"] = "employee-field"
	p.Tables.ReimbursementDetails.Fields["project_relation"] = "project-field"
	p.Approval = &ApprovalSettings{Mode: "manual", TargetIdentity: "bridge", TemplateCode: "template", SubmitterOpenID: "ou_submitter", AllowedAIDecisions: []string{"review"},
		GroupBy:       []ApprovalGroup{{Axis: "project", Role: "reimbursement_details", Field: "project_relation"}},
		InputFields:   map[string]ApprovalInput{"employee": {Role: "reimbursement_details", Field: "employee", Kind: "people"}, "ai_decision": {Role: "review", Field: "decision", Kind: "text"}},
		DetailControl: ApprovalSelector{CustomID: "details"}, FormFields: map[string]ApprovalSelector{"employee": {CustomID: "cardholder"}, "ai_decision": {CustomID: "decision"}}, NodeApprovers: map[string][]string{"node": {"ou_reviewer"}}}
	return p
}

func TestSavedApprovalCredentialsUseExactOriginalIdentityWithoutCurrentApproval(t *testing.T) {
	cfg := Config{FeishuAppID: "new-bridge", FeishuAppSecret: "new-secret", FeishuApprovalAppID: "original", FeishuApprovalAppSecret: "original-secret"}
	id, secret, err := cfg.SavedApprovalCredentials("feishu-app:original")
	if err != nil || id != "original" || secret != "original-secret" {
		t.Fatal("saved target depended on current approval/template configuration")
	}
	for _, scope := range []string{"", "original", "feishu-app:missing", "feishu-app:original "} {
		if _, _, err := cfg.SavedApprovalCredentials(scope); err == nil {
			t.Fatal("unknown saved identity fell back to another credential group")
		}
	}
	cfg.FeishuApprovalAppSecret = ""
	if _, _, err := cfg.SavedApprovalCredentials("feishu-app:original"); err == nil {
		t.Fatal("missing original secret fell back to the bridge")
	}
	cfg.FeishuAppID, cfg.FeishuAppSecret, cfg.FeishuApprovalAppSecret = "original", "same-secret", "same-secret"
	if _, _, err := cfg.SavedApprovalCredentials("feishu-app:original"); err == nil {
		t.Fatal("ambiguous duplicate credential groups were implicitly selected")
	}
}

func TestApprovalConfigurationRequiresExplicitIdentityPolicyAndBindings(t *testing.T) {
	if p := approvalProfile(); p.validate() != nil {
		t.Fatal("valid manual approval rejected")
	}
	for _, change := range []string{"automatic", "identity", "submitter", "policy", "duplicate_policy", "missing_group", "group_field", "duplicate_axis", "missing_timezone", "invalid_timezone", "missing_binding", "duplicate_control", "ambiguous_selector", "unbound_input", "bad_kind", "people_mapping", "currency_on_text", "invalid_node_person"} {
		t.Run(change, func(t *testing.T) {
			p := approvalProfile()
			a := p.Approval
			switch change {
			case "automatic":
				a.Mode = "automatic"
			case "identity":
				a.TargetIdentity = "fallback"
			case "submitter":
				a.SubmitterOpenID = "Display Name"
			case "policy":
				a.AllowedAIDecisions = nil
			case "duplicate_policy":
				a.AllowedAIDecisions = []string{"review", "review"}
			case "group_field":
				a.GroupBy[0].Field = "unknown"
			case "missing_group":
				a.GroupBy = nil
			case "duplicate_axis":
				a.GroupBy = append(a.GroupBy, a.GroupBy[0])
			case "missing_timezone":
				a.GroupBy[0].Period = "month"
			case "invalid_timezone":
				a.GroupBy[0].Period, a.GroupBy[0].Timezone = "month", "not/a-zone"
			case "missing_binding":
				delete(a.FormFields, "employee")
			case "duplicate_control":
				a.FormFields["employee"] = a.FormFields["ai_decision"]
			case "ambiguous_selector":
				a.DetailControl.ID = "also-system-id"
			case "unbound_input":
				a.InputFields["employee"] = ApprovalInput{Role: "reimbursement_details", Field: "unknown", Kind: "people"}
			case "bad_kind":
				a.InputFields["employee"] = ApprovalInput{Role: "reimbursement_details", Field: "employee", Kind: "name_match"}
			case "people_mapping":
				a.TargetIdentity = "approval"
			case "currency_on_text":
				a.InputFields["ai_decision"] = ApprovalInput{Role: "review", Field: "decision", Kind: "text", Currency: "USD"}
			case "invalid_node_person":
				a.NodeApprovers["node"] = []string{"Name"}
			}
			if p.validate() == nil {
				t.Fatal("invalid manual approval configuration accepted")
			}
		})
	}
}

func TestApprovalTransactionInputsAndGroupingRequireReviewedLinkedFacts(t *testing.T) {
	p := approvalProfile()
	p.Approval.InputFields["ai_decision"] = ApprovalInput{Role: "transactions", Field: "transaction_id", Kind: "text"}
	if p.validate() == nil {
		t.Fatal("transaction facts outside AI evidence enabled")
	}
	p.Review.IncludeTransactions = true
	for _, semantic := range []string{"original_amount", "original_currency", "merchant", "transaction_time"} {
		p.Tables.Transactions.Fields[semantic] = semantic + "-field"
	}
	p.Approval.GroupBy = []ApprovalGroup{{Axis: "month", Role: "transactions", Field: "transaction_time", Period: "month", Timezone: "Asia/Shanghai"}}
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	p.Approval.InputFields["ai_decision"] = ApprovalInput{Role: "transactions", Field: "original_amount", Kind: "money", CurrencyField: "original_currency"}
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	p.Approval.InputFields["ai_decision"] = ApprovalInput{Role: "transactions", Field: "original_amount", Kind: "money", Currency: "USD", CurrencyField: "original_currency"}
	if p.validate() == nil {
		t.Fatal("ambiguous money currency accepted")
	}
	p.Approval.InputFields["ai_decision"] = ApprovalInput{Role: "review", Field: "decision", Kind: "text"}
	p.Review.IncludeTransactions = false
	if p.validate() == nil {
		t.Fatal("transaction grouping bypassed reviewed evidence requirement")
	}
}

func TestApprovalDraftAndCredentialSelectionNeverFallBack(t *testing.T) {
	p := testProfile()
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	p.Approval = &ApprovalSettings{Mode: "disabled"}
	if _, err := LoadBusinessProfile(writeProfile(t, p)); err != nil {
		t.Fatal("disabled draft broke existing business")
	}
	p = approvalProfile()
	p.Approval.TargetIdentity = "approval"
	p.Approval.InputFields["employee"] = ApprovalInput{Role: "reimbursement_details", Field: "employee", Kind: "people", PersonMap: map[string]string{"ou_source": "ou_target"}}
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Business: &p, FeishuAppID: "bridge", FeishuAppSecret: "bridge-secret"}
	if _, _, err := cfg.ApprovalCredentials(); err == nil {
		t.Fatal("missing separate identity fell back to bridge")
	}
	cfg.FeishuApprovalAppID, cfg.FeishuApprovalAppSecret = "approval-app", "approval-secret"
	id, secret, err := cfg.ApprovalCredentials()
	if err != nil || id != "approval-app" || secret != "approval-secret" {
		t.Fatal("explicit approval identity not selected")
	}
	p.Approval.TargetIdentity = "bridge"
	id, _, err = cfg.ApprovalCredentials()
	if err != nil || id != "bridge" {
		t.Fatal("explicit bridge identity not selected")
	}
	// Credentials are environment-only, including inside the new section.
	encoded, _ := json.Marshal(p)
	var raw map[string]any
	json.Unmarshal(encoded, &raw)
	raw["approval"].(map[string]any)["app_secret"] = "MUST_NOT_LOAD"
	encoded, _ = json.Marshal(raw)
	path := filepath.Join(t.TempDir(), "invalid.json")
	os.WriteFile(path, encoded, 0600)
	if _, err := LoadBusinessProfile(path); err == nil {
		t.Fatal("approval secret accepted in business file")
	}
}
