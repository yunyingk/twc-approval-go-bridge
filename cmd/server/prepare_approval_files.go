package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	native "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type approvalFileReport struct {
	Kind              string                 `json:"preview_kind"`
	TargetScope       string                 `json:"target_scope"`
	Reviews           []app.ReviewCheck      `json:"records"`
	Inputs            []app.InputCheck       `json:"source_inputs"`
	Uploads           []approvalUploadStatus `json:"uploads"`
	Issues            []string               `json:"issues"`
	Complete          bool                   `json:"file_preparation_complete"`
	FormValidated     bool                   `json:"form_validated"`
	Plans             []approvalPlanPreview  `json:"plans,omitempty"`
	CreationAvailable bool                   `json:"creation_available"`
}
type approvalUploadStatus struct {
	ID       string        `json:"id"`
	RecordID string        `json:"record_id"`
	Input    string        `json:"input"`
	State    string        `json:"state"`
	Runs     int           `json:"runs"`
	Failure  *core.Failure `json:"failure,omitempty"`
}

func runApprovalFilePreparation(ctx context.Context, cfg config.Config, selection string, retryRejected bool, output io.Writer) error {
	ids, err := approvalRecordSelection(selection)
	if err != nil {
		return err
	}
	if cfg.Business == nil || cfg.Business.Approval == nil || cfg.Business.Approval.Mode != "manual" {
		return fmt.Errorf("approval uploads require a selected manual approval configuration")
	}
	appID, secret, err := cfg.ApprovalCredentials()
	if err != nil {
		return err
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
	gate, err := app.NewReviewGate(source, store, scope, cfg.ReviewProvider, cfg.Business.Approval.AllowedAIDecisions)
	if err != nil {
		return err
	}
	fields, err := newApprovalFieldsSource(cfg)
	if err != nil {
		return err
	}
	prepared, err := app.NewPreparedSourceWithUploads(fields, gate, store, "feishu-app:"+appID)
	if err != nil {
		return err
	}
	gateway, target, issue := inspectApprovalTarget(ctx, cfg)
	if issue != "" {
		return fmt.Errorf("approval uploads blocked: %s", issue)
	}
	uploader, err := native.NewFileUploader(appID, secret, nil)
	if err != nil {
		return err
	}
	manager, err := app.NewUploadManager(uploader, store)
	if err != nil {
		return err
	}
	return prepareApprovalFiles(ctx, cfg, ids, retryRejected, prepared, manager, gateway, *target, output)
}

type approvalUploadDraftValidator interface {
	approvalPlanValidator
	ValidateUploadDraft(context.Context, string, []core.Row, []app.UploadPayload) error
}

// This explicit command validates the full selection before uploading anything.
// Each receipt is independently saved; a later file failure retains prior uploads.
func prepareApprovalFiles(ctx context.Context, cfg config.Config, ids []string, retryRejected bool, source *app.PreparedSource, manager *app.UploadManager, gateway approvalUploadDraftValidator, target approvalPreviewTarget, output io.Writer) error {
	inspection, err := source.Inspect(ctx, ids)
	if err != nil {
		return err
	}
	report := approvalFileReport{Kind: "file_preparation", TargetScope: target.Scope, Reviews: inspection.Reviews, Inputs: inspection.Inputs, Uploads: []approvalUploadStatus{}, Issues: []string{}}
	drafts, payloads, err := inspection.UploadDraft(retryRejected)
	if err != nil {
		report.Issues = append(report.Issues, "source_not_ready")
		return json.NewEncoder(output).Encode(report)
	}
	plans, err := core.BuildPlans(approvalPlanOptions(cfg, target), drafts)
	if err != nil {
		report.Issues = append(report.Issues, "plan_inputs_invalid")
		return json.NewEncoder(output).Encode(report)
	}
	for _, plan := range plans {
		members := map[string]bool{}
		for _, row := range plan.Rows {
			members[row.RecordID] = true
		}
		groupFiles := []app.UploadPayload{}
		for _, payload := range payloads {
			if members[payload.Request.RecordID] {
				groupFiles = append(groupFiles, payload)
			}
		}
		if err := gateway.ValidateUploadDraft(ctx, target.Version, plan.Rows, groupFiles); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report.Issues = append(report.Issues, "target_form_invalid")
			return json.NewEncoder(output).Encode(report)
		}
	}
	seen := map[string]bool{}
	for _, payload := range payloads {
		if seen[payload.Request.ID] {
			continue
		}
		seen[payload.Request.ID] = true
		attempt, err := manager.Upload(ctx, payload, retryRejected)
		status := approvalUploadStatus{ID: payload.Request.ID, RecordID: payload.Request.RecordID, Input: payload.Semantic, State: "unavailable", Runs: len(attempt.Runs)}
		if len(attempt.Runs) > 0 {
			last := attempt.Runs[len(attempt.Runs)-1]
			status.State, status.Failure = last.State, last.Failure
		}
		report.Uploads = append(report.Uploads, status)
		if err != nil {
			report.Issues = append(report.Issues, "upload_stopped")
			return json.NewEncoder(output).Encode(report)
		}
	}
	// Uploads are not a freeze. Observe current source/AI evidence again before
	// presenting complete preparation or a validated executable-plan summary.
	inspection, err = source.Inspect(ctx, ids)
	if err != nil {
		return err
	}
	report.Reviews, report.Inputs = inspection.Reviews, inspection.Inputs
	if _, err := inspection.Rows(); err != nil {
		report.Issues = append(report.Issues, "source_changed_after_upload")
	} else {
		report.Complete = true
		plans, issue := previewApprovalPlans(ctx, cfg, inspection, target, gateway)
		if issue != "" {
			report.Issues = append(report.Issues, issue)
		}
		report.Plans, report.FormValidated = plans, len(plans) > 0
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return json.NewEncoder(output).Encode(report)
}
