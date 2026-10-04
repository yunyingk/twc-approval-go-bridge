package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	native "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
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
	FormInputsChecked bool                   `json:"form_inputs_checked"`
	CreationAvailable bool                   `json:"creation_available"`
}
type approvalPreviewTarget struct {
	Scope    string `json:"scope"`
	Template string `json:"template"`
	Version  string `json:"configuration_version"`
}

// This preflight never submits or changes state. Current review facts and
// target template metadata are separate from complete form
// preparation, which will add typed source inputs and uploaded file references.
func runApprovalPreview(ctx context.Context, cfg config.Config, selection string, output io.Writer) error {
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
	report.Records, err = gate.Check(ctx, strings.Split(selection, ","))
	if err != nil {
		return err
	}
	if report.Mode == "manual" {
		target, issue := inspectApprovalTarget(ctx, cfg)
		report.Target = target
		if issue != "" {
			report.Configuration = append(report.Configuration, issue)
		}
	}
	// A green AI/template precheck is not an executable approval plan.
	report.Configuration = append(report.Configuration, "form_source_preparation_pending")
	return json.NewEncoder(output).Encode(report)
}

func inspectApprovalTarget(ctx context.Context, cfg config.Config) (*approvalPreviewTarget, string) {
	appID, secret, err := cfg.ApprovalCredentials()
	if err != nil {
		return nil, "target_credentials_missing"
	}
	client, err := native.NewInstanceClient(appID, secret, nil)
	if err != nil {
		return nil, "target_configuration_invalid"
	}
	a := cfg.Business.Approval
	binding := native.DetailFormBinding{Detail: native.ControlSelector{ID: a.DetailControl.ID, CustomID: a.DetailControl.CustomID}, Fields: map[string]native.ControlSelector{}}
	for semantic, selector := range a.FormFields {
		binding.Fields[semantic] = native.ControlSelector{ID: selector.ID, CustomID: selector.CustomID}
	}
	gateway, err := native.NewInstanceGateway(client, a.TemplateCode, binding, a.NodeApprovers)
	if err != nil {
		return nil, "target_configuration_invalid"
	}
	target, err := gateway.Describe(ctx)
	if err != nil {
		return nil, "target_template_unavailable"
	}
	return &approvalPreviewTarget{Scope: target.Scope, Template: target.Template, Version: target.ConfigurationVersion}, ""
}
