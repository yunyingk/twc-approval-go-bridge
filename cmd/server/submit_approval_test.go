package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type commandBatchGateway struct {
	commandRequestRenderer
	creates int
}

func (*commandBatchGateway) Describe(context.Context) (app.Target, error) {
	return app.Target{Scope: "feishu-app:bridge", Template: "template", ConfigurationVersion: "version"}, nil
}
func (g *commandBatchGateway) ValidatePlan(ctx context.Context, plan core.Plan) error {
	_, err := g.PrepareRequest(ctx, plan)
	return err
}
func (*commandBatchGateway) Create(context.Context, core.Plan) (core.Instance, error) {
	panic("must use prepared body")
}
func (g *commandBatchGateway) CreatePrepared(_ context.Context, p core.Plan, _ core.RequestArtifact) (core.Instance, error) {
	g.creates++
	return core.Instance{ID: "instance-" + p.ID, UUID: p.ID, TargetScope: p.TargetScope, Template: p.Template, SubmitterID: p.Submitter.ID, Status: "pending"}, nil
}
func (*commandBatchGateway) Lookup(context.Context, core.Plan) (core.Instance, error) {
	panic("local diagnostics must not query native instances")
}

func commandBatchFixture(t *testing.T) (config.Config, *app.BatchService, core.PreparedBatch, *state.Files, *commandBatchGateway) {
	t.Helper()
	cfg, source, _, _, store, uploader, _ := preparationFixture(t)
	inspection, _ := source.Inspect(context.Background(), []string{"a", "b"})
	_, files, err := inspection.UploadDraft(false)
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := app.NewUploadManager(uploader, store)
	for _, file := range files {
		if _, err := manager.Upload(context.Background(), file, false); err != nil {
			t.Fatal(err)
		}
	}
	gateway := &commandBatchGateway{}
	target := approvalPreviewTarget{Scope: gateway.TargetScope(), Template: "template", Version: "version"}
	options := approvalPlanOptions(cfg, target)
	preparer, _ := app.NewRequestPreparer(source, gateway, store, options)
	result, err := preparer.Prepare(context.Background(), []string{"a", "b"})
	if err != nil || result.Batch == nil {
		t.Fatalf("prepare command batch: %v / %s", err, result.Issue)
	}
	path, _ := store.ApprovalPreparationPath(result.Batch.ID)
	cfg.StateDir, cfg.FeishuAppID, cfg.FeishuAppSecret = filepath.Dir(path), "bridge", "secret"
	cfg.Business.Approval.TargetIdentity = "bridge"
	service, _ := app.NewBatchService(source, gateway, store, options)
	return cfg, service, *result.Batch, store, gateway
}

func commandStateHashes(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string][32]byte{}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(root, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[file.Name()] = sha256.Sum256(raw)
	}
	return result
}

func TestApprovalCommandsOnlyExposeSafeBatchMappingsAndReadUnsentStateWithoutCredentials(t *testing.T) {
	cfg, service, batch, _, gateway := commandBatchFixture(t)
	before := commandStateHashes(t, cfg.StateDir)
	local := config.Config{StateDir: cfg.StateDir}
	for _, run := range []func(context.Context, config.Config, string, *bytes.Buffer) error{
		func(ctx context.Context, c config.Config, id string, out *bytes.Buffer) error {
			return runApprovalStatus(ctx, c, id, out)
		},
		func(ctx context.Context, c config.Config, id string, out *bytes.Buffer) error {
			return runApprovalReconciliation(ctx, c, id, out)
		},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), local, batch.ID, &output); err != nil || strings.Contains(output.String(), "PRIVATE") || strings.Contains(output.String(), "9007199254740993") {
			t.Fatal("local/unsent lookup required credentials or exposed a private request")
		}
	}
	if !reflect.DeepEqual(before, commandStateHashes(t, cfg.StateDir)) {
		t.Fatal("local/unsent state check wrote files")
	}
	var output bytes.Buffer
	if err := submitApprovalBatch(context.Background(), service, batch.ID, &output); err != nil || gateway.creates != 2 || strings.Contains(output.String(), "PRIVATE") {
		t.Fatal("command did not submit exact saved groups safely")
	}
	var report struct{ app.BatchResult }
	if json.Unmarshal(output.Bytes(), &report) != nil || !report.Status.AllInstancesKnown || report.Status.Plans[0].Audit == nil {
		t.Fatal("command lost instance/audit mapping")
	}
	// Repeating the top-level command returns the original mappings before source
	// or target assembly, even though this test has no usable real template setup.
	output.Reset()
	if err := runApprovalSubmission(context.Background(), cfg, batch.ID, &output); err != nil || gateway.creates != 2 {
		t.Fatal("known command rerun constructed a new provider/source/target")
	}
	before = commandStateHashes(t, cfg.StateDir)
	output.Reset()
	if err := runApprovalReconciliation(context.Background(), local, batch.ID, &output); err == nil || !strings.Contains(output.String(), "original_target_credentials_unavailable") {
		t.Fatal("missing original target credentials did not block lookup")
	}
	if !reflect.DeepEqual(before, commandStateHashes(t, cfg.StateDir)) {
		t.Fatal("missing-credential lookup changed persisted mapping")
	}
}

type privateFailureSubmitter struct{ result app.BatchResult }

func (s privateFailureSubmitter) Submit(context.Context, string) (app.BatchResult, error) {
	return s.result, errors.New("PRIVATE_UPSTREAM_REQUEST_SECRET")
}

func TestApprovalSubmissionCommandSanitizesFailureAndReportsPartialState(t *testing.T) {
	var output bytes.Buffer
	result := app.BatchResult{Status: app.BatchStatus{ID: strings.Repeat("a", 64), Plans: []app.PlanStatus{{ID: "uuid", Phase: "unknown"}}}, Issue: "submission_failed"}
	err := submitApprovalBatch(context.Background(), privateFailureSubmitter{result}, result.Status.ID, &output)
	if err == nil || strings.Contains(err.Error()+output.String(), "PRIVATE") || !strings.Contains(output.String(), "unknown") {
		t.Fatal("partial failure lost diagnostic status or exposed raw private error")
	}
}

func TestApprovalOperationGuardsDoNotCreateStateOrUseUnconfiguredSubmission(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	cfg := config.Config{StateDir: root}
	for _, id := range []string{"", "all", "uuid", strings.Repeat("A", 64), " " + strings.Repeat("a", 64)} {
		for _, run := range []func(context.Context, config.Config, string, *bytes.Buffer) error{
			func(ctx context.Context, c config.Config, id string, out *bytes.Buffer) error {
				return runApprovalSubmission(ctx, c, id, out)
			},
			func(ctx context.Context, c config.Config, id string, out *bytes.Buffer) error {
				return runApprovalStatus(ctx, c, id, out)
			},
			func(ctx context.Context, c config.Config, id string, out *bytes.Buffer) error {
				return runApprovalReconciliation(ctx, c, id, out)
			},
			func(ctx context.Context, c config.Config, id string, out *bytes.Buffer) error {
				return runApprovalAbandonment(ctx, c, id, out)
			},
		} {
			if err := run(context.Background(), cfg, id, &bytes.Buffer{}); err == nil {
				t.Fatal("invalid preparation ID reached an operation")
			}
		}
	}
	if err := runApprovalSubmission(context.Background(), cfg, strings.Repeat("a", 64), &bytes.Buffer{}); err == nil {
		t.Fatal("unconfigured submission reached state/network")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("guarded command created state")
	}
}

func TestFailedCreationStillRequiresOriginalLookupAfterAnEarlierQueryFailure(t *testing.T) {
	cfg, _, batch, store, gateway := commandBatchFixture(t)
	ctx := context.Background()
	if _, err := store.BeginApprovalBatch(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	prepared := batch.Plans[0]
	if _, claimed, err := store.ClaimApprovalSend(ctx, prepared.Plan.SourceScope, prepared.Plan.ID, prepared.AuditReference(batch.ID)); err != nil || !claimed {
		t.Fatal("could not save test send intent")
	}
	_, err := store.UpdateApproval(ctx, prepared.Plan.SourceScope, prepared.Plan.ID, func(a *core.Attempt) error {
		a.Phase = "failed"
		a.Failure = &core.Failure{Phase: "creation", Code: "failed"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdateApproval(ctx, prepared.Plan.SourceScope, prepared.Plan.ID, func(a *core.Attempt) error {
		a.Failure = &core.Failure{Phase: "reconciliation", Code: "lookup_failed"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runApprovalReconciliation(ctx, config.Config{StateDir: cfg.StateDir}, batch.ID, &output); err == nil || !strings.Contains(output.String(), "original_target_credentials_unavailable") || strings.Contains(output.String(), "not_submitted") || gateway.creates != 0 {
		t.Fatal("failed UUID was mistaken for unsent after a lookup failure")
	}
}
