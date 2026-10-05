package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	native "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type approvalPreflight struct {
	Kind              string                 `json:"preview_kind"`
	SourceScope       string                 `json:"source_scope"`
	ReviewProvider    string                 `json:"review_provider"`
	Mode              string                 `json:"approval_mode"`
	Configuration     []string               `json:"configuration_issues"`
	Target            *approvalPreviewTarget `json:"target,omitempty"`
	Records           []app.ReviewCheck      `json:"records"`
	Inputs            []app.InputCheck       `json:"source_inputs,omitempty"`
	Plans             []approvalPlanPreview  `json:"plans,omitempty"`
	FormInputsChecked bool                   `json:"form_inputs_checked"`
	FormValidated     bool                   `json:"form_validated"`
	CreationAvailable bool                   `json:"creation_available"`
}
type approvalPlanPreview struct {
	ID        string   `json:"id"`
	Revision  string   `json:"revision"`
	RecordIDs []string `json:"record_ids"`
	Axes      []string `json:"axes"`
}
type approvalPreviewTarget struct {
	Scope    string `json:"scope"`
	Template string `json:"template"`
	Version  string `json:"configuration_version"`
}

// This preflight never submits or changes state. Typed inputs and current AI
// evidence may produce validated plan summaries, without exposing form values.
func runApprovalPreview(ctx context.Context, cfg config.Config, selection string, output io.Writer) error {
	ids := strings.Split(selection, ",")
	selected := map[string]bool{}
	for _, id := range ids {
		if id == "" || id != strings.TrimSpace(id) || selected[id] {
			return fmt.Errorf("approval preview requires unique explicit selected record IDs")
		}
		selected[id] = true
	}
	store, err := state.OpenFiles(cfg.StateDir)
	if err != nil {
		return err
	}
	source, err := newReviewSource(cfg)
	if err != nil {
		return err
	}
	scope := "feishu:" + cfg.ReceiptBaseToken + ":" + cfg.ReceiptTableID
	var allowed []string
	report := approvalPreflight{Kind: "preflight", SourceScope: scope, ReviewProvider: cfg.ReviewProvider, Mode: "disabled", Configuration: []string{}}
	if cfg.Business == nil || cfg.Business.Approval == nil {
		report.Configuration = append(report.Configuration, "approval_not_configured")
	} else if cfg.Business.Approval.Mode == "disabled" {
		report.Configuration = append(report.Configuration, "approval_disabled")
	} else {
		report.Mode = cfg.Business.Approval.Mode
		allowed = cfg.Business.Approval.AllowedAIDecisions
	}
	gate, err := app.NewReviewGate(source, store, scope, cfg.ReviewProvider, allowed)
	if err != nil {
		return err
	}
	if report.Mode == "manual" {
		gateway, target, issue := inspectApprovalTarget(ctx, cfg)
		report.Target = target
		if issue != "" {
			report.Configuration = append(report.Configuration, issue)
		}
		fields, sourceErr := newApprovalFieldsSource(cfg)
		if sourceErr == nil {
			prepared, err := app.NewPreparedSource(fields, gate)
			if err != nil {
				return err
			}
			inspection, err := prepared.Inspect(ctx, ids)
			if err == nil {
				report.Records, report.Inputs, report.FormInputsChecked = inspection.Reviews, inspection.Inputs, true
				if gateway != nil && target != nil {
					plans, issue := previewApprovalPlans(ctx, cfg, inspection, *target, gateway)
					if issue != "" {
						report.Configuration = append(report.Configuration, issue)
					}
					report.Plans, report.FormValidated = plans, len(plans) > 0
				}
			} else {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				report.Configuration = append(report.Configuration, "form_source_unavailable")
			}
		} else if issue != "target_credentials_missing" {
			report.Configuration = append(report.Configuration, "form_source_configuration_invalid")
		}
	}
	if !report.FormInputsChecked {
		report.Records, err = gate.Check(ctx, ids)
		if err != nil {
			return err
		}
		report.Configuration = append(report.Configuration, "form_source_preparation_pending")
	}
	// Even a validated plan summary is a read-only preview; no creation method is called.
	return json.NewEncoder(output).Encode(report)
}

func inspectApprovalTarget(ctx context.Context, cfg config.Config) (*native.InstanceGateway, *approvalPreviewTarget, string) {
	appID, secret, err := cfg.ApprovalCredentials()
	if err != nil {
		return nil, nil, "target_credentials_missing"
	}
	client, err := native.NewInstanceClient(appID, secret, nil)
	if err != nil {
		return nil, nil, "target_configuration_invalid"
	}
	a := cfg.Business.Approval
	binding := native.DetailFormBinding{Detail: native.ControlSelector{ID: a.DetailControl.ID, CustomID: a.DetailControl.CustomID}, Fields: map[string]native.ControlSelector{}}
	for semantic, selector := range a.FormFields {
		binding.Fields[semantic] = native.ControlSelector{ID: selector.ID, CustomID: selector.CustomID}
	}
	gateway, err := native.NewInstanceGateway(client, a.TemplateCode, binding, a.NodeApprovers)
	if err != nil {
		return nil, nil, "target_configuration_invalid"
	}
	target, err := gateway.Describe(ctx)
	if err != nil {
		return nil, nil, "target_template_unavailable"
	}
	return gateway, &approvalPreviewTarget{Scope: target.Scope, Template: target.Template, Version: target.ConfigurationVersion}, ""
}

func newApprovalFieldsSource(cfg config.Config) (*base.ApprovalFieldsSource, error) {
	appID, _, err := cfg.ApprovalCredentials()
	if err != nil {
		return nil, err
	}
	a := cfg.Business.Approval
	details, transactions := cfg.Business.Tables.ReimbursementDetails, cfg.Business.Tables.Transactions
	if details.BaseToken != cfg.ReceiptBaseToken || details.TableID != cfg.ReceiptTableID {
		return nil, fmt.Errorf("approval source must match selected review source")
	}
	binding := base.ApprovalSourceBinding{BaseToken: details.BaseToken, DetailTableID: details.TableID, TargetScope: "feishu-app:" + appID, Inputs: map[string]base.ApprovalInputBinding{}}
	roleFields := func(role string) map[string]string {
		if role == "transactions" {
			return transactions.Fields
		}
		return details.Fields
	}
	for semantic, input := range a.InputFields {
		fieldID := roleFields(input.Role)[input.Field]
		if input.Role == "review" {
			fieldID = input.Field
		}
		binding.Inputs[semantic] = base.ApprovalInputBinding{Role: input.Role, FieldID: fieldID, Kind: input.Kind, Currency: input.Currency, CurrencyFieldID: roleFields(input.Role)[input.CurrencyField], PersonMap: input.PersonMap}
	}
	usesTransactions := false
	for _, input := range a.InputFields {
		usesTransactions = usesTransactions || input.Role == "transactions"
	}
	for _, group := range a.GroupBy {
		usesTransactions = usesTransactions || group.Role == "transactions"
		binding.Groups = append(binding.Groups, base.ApprovalGroupBinding{Axis: group.Axis, Role: group.Role, FieldID: roleFields(group.Role)[group.Field], Period: group.Period, Timezone: group.Timezone})
	}
	if usesTransactions {
		if !cfg.Business.Review.IncludeTransactions || transactions.BaseToken != details.BaseToken {
			return nil, fmt.Errorf("approval transactions must be reviewed in the same Base")
		}
		binding.TransactionTableID, binding.TransactionRelationFieldID = transactions.TableID, details.Fields["transaction_relation"]
	}
	return base.NewApprovalFieldsSource(base.NewLedgerClient(cfg.FeishuAppID, cfg.FeishuAppSecret), binding)
}

type approvalPlanValidator interface {
	ValidatePlan(context.Context, core.Plan) error
}

func previewApprovalPlans(ctx context.Context, cfg config.Config, inspection app.SourceInspection, target approvalPreviewTarget, gateway approvalPlanValidator) ([]approvalPlanPreview, string) {
	rows, err := inspection.Rows()
	if err != nil {
		return nil, ""
	} // Row diagnostics already identify every hold.
	a := cfg.Business.Approval
	axes := []string{}
	for _, group := range a.GroupBy {
		axes = append(axes, group.Axis)
	}
	plans, err := core.BuildPlans(core.Options{SourceScope: "feishu:" + cfg.ReceiptBaseToken + ":" + cfg.ReceiptTableID, TargetScope: target.Scope, Template: target.Template, ConfigurationVersion: target.Version,
		DepartmentID: a.DepartmentID, Submitter: core.Identity{Scope: target.Scope, ID: a.SubmitterOpenID}, Axes: axes, AllowedDecisions: a.AllowedAIDecisions}, rows)
	if err != nil {
		return nil, "plan_inputs_invalid"
	}
	previews := make([]approvalPlanPreview, 0, len(plans))
	for _, plan := range plans {
		if err := gateway.ValidatePlan(ctx, plan); err != nil {
			return nil, "target_form_invalid"
		}
		preview := approvalPlanPreview{ID: plan.ID, Revision: plan.Revision, Axes: plan.Axes, RecordIDs: []string{}}
		for _, row := range plan.Rows {
			preview.RecordIDs = append(preview.RecordIDs, row.RecordID)
		}
		previews = append(previews, preview)
	}
	return previews, ""
}
