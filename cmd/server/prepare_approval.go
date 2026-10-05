package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type approvalRequestPreview struct {
	PlanID        string   `json:"plan_id"`
	Revision      string   `json:"revision"`
	Records       []string `json:"record_ids"`
	RequestFormat string   `json:"request_format"`
	RequestHash   string   `json:"request_sha256"`
}
type approvalPreparationReport struct {
	Kind              string                   `json:"preview_kind"`
	Target            approvalPreviewTarget    `json:"target"`
	Reviews           []app.ReviewCheck        `json:"records"`
	Inputs            []app.InputCheck         `json:"source_inputs"`
	Issues            []string                 `json:"issues"`
	ID                string                   `json:"preparation_id,omitempty"`
	AuditFile         string                   `json:"private_audit_file,omitempty"`
	Plans             []approvalRequestPreview `json:"plans,omitempty"`
	Complete          bool                     `json:"preparation_complete"`
	CreationAvailable bool                     `json:"creation_available"`
}

func runApprovalRequestPreparation(ctx context.Context, cfg config.Config, selection string, output io.Writer) error {
	ids, err := approvalRecordSelection(selection)
	if err != nil {
		return err
	}
	if cfg.Business == nil || cfg.Business.Approval == nil || cfg.Business.Approval.Mode != "manual" {
		return fmt.Errorf("approval request preparation requires a selected manual approval configuration")
	}
	appID, _, err := cfg.ApprovalCredentials()
	if err != nil {
		return err
	}
	store, err := state.OpenFiles(cfg.StateDir)
	if err != nil {
		return err
	}
	reviews, err := newReviewSource(cfg)
	if err != nil {
		return err
	}
	scope := "feishu:" + cfg.ReceiptBaseToken + ":" + cfg.ReceiptTableID
	gate, err := app.NewReviewGate(reviews, store, scope, cfg.ReviewProvider, cfg.Business.Approval.AllowedAIDecisions)
	if err != nil {
		return err
	}
	fields, err := newApprovalFieldsSource(cfg)
	if err != nil {
		return err
	}
	source, err := app.NewPreparedSourceWithUploads(fields, gate, store, "feishu-app:"+appID)
	if err != nil {
		return err
	}
	gateway, target, issue := inspectApprovalTarget(ctx, cfg)
	if issue != "" {
		return fmt.Errorf("approval request preparation blocked: %s", issue)
	}
	return prepareApprovalRequests(ctx, cfg, ids, source, gateway, *target, store, output)
}

type approvalPreparationStore interface {
	app.PreparationStore
	ApprovalPreparationPath(string) (string, error)
}

// Diagnostics never print the private AI facts, native form or file codes.
// The whole validated batch is saved separately for local request review.
func prepareApprovalRequests(ctx context.Context, cfg config.Config, ids []string, source app.InspectionSource, renderer app.RequestRenderer, target approvalPreviewTarget, store approvalPreparationStore, output io.Writer) error {
	preparer, err := app.NewRequestPreparer(source, renderer, store, approvalPlanOptions(cfg, target))
	if err != nil {
		return err
	}
	result, err := preparer.Prepare(ctx, ids)
	if err != nil {
		return err
	}
	report := approvalPreparationReport{Kind: "request_preparation", Target: target, Reviews: result.Inspection.Reviews, Inputs: result.Inspection.Inputs, Issues: []string{}}
	if result.Issue != "" {
		report.Issues = append(report.Issues, result.Issue)
	}
	if result.Batch != nil {
		report.ID = result.Batch.ID
		report.AuditFile, err = store.ApprovalPreparationPath(report.ID)
		if err != nil {
			return err
		}
		for _, prepared := range result.Batch.Plans {
			preview := approvalRequestPreview{PlanID: prepared.Plan.ID, Revision: prepared.Plan.Revision, RequestFormat: prepared.Request.Format, RequestHash: prepared.Request.Hash, Records: []string{}}
			for _, row := range prepared.Plan.Rows {
				preview.Records = append(preview.Records, row.RecordID)
			}
			report.Plans = append(report.Plans, preview)
		}
		report.Complete = true
	}
	return json.NewEncoder(output).Encode(report)
}
