package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	native "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func approvalPreparationID(id string) error {
	digest, err := hex.DecodeString(id)
	if err != nil || len(digest) != sha256.Size || id != hex.EncodeToString(digest) {
		return fmt.Errorf("approval operation requires one saved lowercase preparation SHA-256 ID")
	}
	return nil
}

func runApprovalSubmission(ctx context.Context, cfg config.Config, id string, output io.Writer) error {
	if err := approvalPreparationID(id); err != nil {
		return err
	}
	if cfg.Business == nil || cfg.Business.Approval == nil || cfg.Business.Approval.Mode != "manual" {
		return fmt.Errorf("approval submission requires a selected manual approval configuration")
	}
	appID, _, err := cfg.ApprovalCredentials()
	if err != nil {
		return err
	}
	store, err := state.OpenFiles(cfg.StateDir)
	if err != nil {
		return err
	}
	batch, err := store.ReadApprovalPreparation(ctx, id)
	if err != nil {
		return fmt.Errorf("saved approval preparation is unavailable or invalid")
	}
	first := batch.Plans[0].Plan
	if first.SourceScope != "feishu:"+cfg.ReceiptBaseToken+":"+cfg.ReceiptTableID || first.TargetScope != "feishu-app:"+appID {
		return fmt.Errorf("approval submission source or target differs from the saved preparation")
	}
	// A duplicate confirmed submission is only a historical mapping read. It
	// must not depend on the old template/source still being accessible today.
	status, err := app.ReadBatchStatus(ctx, store, id)
	if err != nil {
		return fmt.Errorf("saved approval status is unavailable or invalid")
	}
	for n, plan := range status.Plans {
		if plan.Audit != nil && *plan.Audit != batch.Plans[n].AuditReference(id) {
			return fmt.Errorf("approval submission audit conflicts with the saved attempt")
		}
		if plan.Phase != "not_started" && plan.Audit == nil {
			return fmt.Errorf("approval submission cannot reinterpret an unaudited historical attempt")
		}
		if plan.Phase == "submitting" || plan.Phase == "unknown" || plan.Phase == "failed" {
			return writeApprovalOperation(output, "submission", app.BatchResult{Status: status, Issue: "reconciliation_required"}, core.ErrReconcile)
		}
	}
	if status.AllInstancesKnown {
		return writeApprovalOperation(output, "submission", app.BatchResult{Status: status}, nil)
	}
	source, err := newApprovalPreparedSource(cfg, store, first.TargetScope)
	if err != nil {
		return fmt.Errorf("approval submission source configuration is invalid")
	}
	gateway, target, issue := inspectApprovalTarget(ctx, cfg)
	if issue != "" {
		return writeApprovalOperation(output, "submission", app.BatchResult{Status: status, Issue: issue}, core.ErrPlanChanged)
	}
	service, err := app.NewBatchService(source, gateway, store, approvalPlanOptions(cfg, *target))
	if err != nil {
		return fmt.Errorf("approval submission configuration is invalid")
	}
	return submitApprovalBatch(ctx, service, id, output)
}

type approvalBatchSubmitter interface {
	Submit(context.Context, string) (app.BatchResult, error)
}

func submitApprovalBatch(ctx context.Context, service approvalBatchSubmitter, id string, output io.Writer) error {
	result, err := service.Submit(ctx, id)
	if result.Status.ID == "" {
		return fmt.Errorf("approval submission state is unavailable or invalid")
	}
	return writeApprovalOperation(output, "submission", result, err)
}

func writeApprovalOperation(output io.Writer, operation string, result app.BatchResult, cause error) error {
	if err := json.NewEncoder(output).Encode(struct {
		Operation string `json:"operation"`
		app.BatchResult
	}{operation, result}); err != nil {
		return err
	}
	if cause != nil {
		issue := result.Issue
		if issue == "" {
			issue = "operation_failed"
		}
		// Never send raw source/provider errors or private request values to logs.
		return fmt.Errorf("approval %s stopped: %s", operation, issue)
	}
	return nil
}

func runApprovalStatus(ctx context.Context, cfg config.Config, id string, output io.Writer) error {
	if err := approvalPreparationID(id); err != nil {
		return err
	}
	store, err := state.OpenFiles(cfg.StateDir)
	if err != nil {
		return err
	}
	status, err := app.ReadBatchStatus(ctx, store, id)
	if err != nil {
		return fmt.Errorf("saved approval status is unavailable or invalid")
	}
	return writeApprovalOperation(output, "local_status", app.BatchResult{Status: status}, nil)
}

func runApprovalReconciliation(ctx context.Context, cfg config.Config, id string, output io.Writer) error {
	if err := approvalPreparationID(id); err != nil {
		return err
	}
	store, err := state.OpenFiles(cfg.StateDir)
	if err != nil {
		return err
	}
	status, err := app.ReadBatchStatus(ctx, store, id)
	if err != nil {
		return fmt.Errorf("saved approval status is unavailable or invalid")
	}
	needsLookup := false
	for _, plan := range status.Plans {
		attempt := core.Attempt{Phase: plan.Phase, Failure: plan.Failure}
		needsLookup = needsLookup || (plan.Phase != "not_started" && !attempt.NotSent())
	}
	if !needsLookup {
		return writeApprovalOperation(output, "uuid_reconciliation", app.BatchResult{Status: status, Issue: "not_submitted"}, nil)
	}
	appID, secret, err := cfg.SavedApprovalCredentials(status.TargetScope)
	if err != nil {
		return writeApprovalOperation(output, "uuid_reconciliation", app.BatchResult{Status: status, Issue: "original_target_credentials_unavailable"}, err)
	}
	client, err := native.NewInstanceClient(appID, secret, nil)
	if err != nil {
		return fmt.Errorf("original approval lookup client configuration is invalid")
	}
	lookup, err := native.NewInstanceLookupGateway(client)
	if err != nil {
		return err
	}
	result, err := app.ReconcileBatch(ctx, store, lookup, id)
	if result.Status.ID == "" {
		return fmt.Errorf("approval reconciliation state is unavailable or invalid")
	}
	return writeApprovalOperation(output, "uuid_reconciliation", result, err)
}

func runApprovalAbandonment(ctx context.Context, cfg config.Config, id string, output io.Writer) error {
	if err := approvalPreparationID(id); err != nil {
		return err
	}
	store, err := state.OpenFiles(cfg.StateDir)
	if err != nil {
		return err
	}
	if _, err := store.AbandonApprovalReservations(ctx, id); err != nil {
		return fmt.Errorf("unsent approval reservation abandonment failed")
	}
	status, err := app.ReadBatchStatus(ctx, store, id)
	if err != nil {
		return fmt.Errorf("saved approval status is unavailable or invalid")
	}
	return writeApprovalOperation(output, "abandon_unsent", app.BatchResult{Status: status}, nil)
}
