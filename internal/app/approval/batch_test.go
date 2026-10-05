package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type batchGateway struct {
	store   *state.Files
	batchID string
	creates atomic.Int64
	lookups atomic.Int64
	renders atomic.Int64
	failAt  int64
	after   func(core.Plan)
	lookup  func(core.Plan) (core.Instance, error)
}

func (*batchGateway) TargetScope() string { return "target" }
func (*batchGateway) Describe(context.Context) (app.Target, error) {
	return app.Target{Scope: "target", Template: "template", ConfigurationVersion: "version"}, nil
}
func (g *batchGateway) ValidatePlan(ctx context.Context, plan core.Plan) error {
	_, err := g.PrepareRequest(ctx, plan)
	return err
}
func (g *batchGateway) PrepareRequest(_ context.Context, plan core.Plan) (core.RequestArtifact, error) {
	g.renders.Add(1)
	body, err := json.Marshal(struct {
		UUID string
		Rows []core.Row
	}{plan.ID, plan.Rows})
	if err != nil {
		return core.RequestArtifact{}, err
	}
	return core.NewRequestArtifact("test.native.body.v1", body)
}
func (*batchGateway) Create(context.Context, core.Plan) (core.Instance, error) {
	panic("batch creation must use the exact prepared body")
}
func (g *batchGateway) CreatePrepared(ctx context.Context, plan core.Plan, artifact core.RequestArtifact) (core.Instance, error) {
	call := g.creates.Add(1)
	current, err := g.PrepareRequest(ctx, plan)
	if err != nil || current.Hash != artifact.Hash {
		return core.Instance{}, core.ErrRejected
	}
	attempt, err := g.store.ReadApproval(ctx, plan.SourceScope, plan.ID)
	if err != nil || attempt.Phase != "submitting" || attempt.Audit == nil || *attempt.Audit != (core.PreparedPlan{Plan: plan, Request: artifact}).AuditReference(g.batchID) {
		return core.Instance{}, errors.New("missing durable exact audit/send intent")
	}
	all, err := g.store.ApprovalAttempts(ctx, plan.SourceScope)
	if err != nil || len(all) != 2 {
		return core.Instance{}, errors.New("send preceded whole-batch reservation")
	}
	if g.after != nil {
		g.after(plan)
	}
	if g.failAt == call {
		return core.Instance{}, errors.New("PRIVATE_LOST_RESPONSE")
	}
	return instance(plan, "pending", false), nil
}
func (g *batchGateway) Lookup(_ context.Context, plan core.Plan) (core.Instance, error) {
	g.lookups.Add(1)
	if g.lookup != nil {
		return g.lookup(plan)
	}
	return instance(plan, "pending", true), nil
}

func batchOptions() core.Options {
	return core.Options{SourceScope: "source", TargetScope: "target", Template: "template", ConfigurationVersion: "version", Submitter: core.Identity{Scope: "target", ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}}
}
func batchSourceFixture(t *testing.T) (*app.PreparedSource, *typedFields, *gateSource) {
	t.Helper()
	source, fields, reviews := preparedSourceFixture(t)
	fields.onRead = func(_ int, checks []app.InputCheck) []app.InputCheck {
		for n := range checks {
			checks[n].Row.GroupValues["project"] = checks[n].RecordID
		}
		return checks
	}
	return source, fields, reviews
}
func batchFixture(t *testing.T) (*app.BatchService, core.PreparedBatch, *batchGateway, *state.Files, *typedFields, *gateSource) {
	t.Helper()
	source, fields, reviews := batchSourceFixture(t)
	store, _ := state.NewFiles(t.TempDir())
	gateway := &batchGateway{store: store}
	preparer, err := app.NewRequestPreparer(source, gateway, store, batchOptions())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(context.Background(), []string{"a", "b"})
	if err != nil || prepared.Batch == nil {
		t.Fatalf("prepare batch: %v / %s", err, prepared.Issue)
	}
	gateway.batchID = prepared.Batch.ID
	service, err := app.NewBatchService(source, gateway, store, batchOptions())
	if err != nil {
		t.Fatal(err)
	}
	return service, *prepared.Batch, gateway, store, fields, reviews
}

func TestBatchSubmissionPersistsExactAuditAndReservesAllGroupsBeforeSending(t *testing.T) {
	service, batch, gateway, store, _, reviews := batchFixture(t)
	result, err := service.Submit(context.Background(), batch.ID)
	if err != nil || !result.Status.AllInstancesKnown || gateway.creates.Load() != 2 {
		t.Fatalf("audited batch did not submit: %v / %s", err, result.Issue)
	}
	for n, status := range result.Status.Plans {
		if status.Phase != "pending" || status.Audit == nil || *status.Audit != batch.Plans[n].AuditReference(batch.ID) {
			t.Fatal("confirmed attempt lost original audit mapping")
		}
	}
	reads, renders := reviews.reads, gateway.renders.Load()
	// Historical mapping reuse must not require unchanged current AI facts.
	reviews.amount = "different facts"
	result, err = service.Submit(context.Background(), batch.ID)
	if err != nil || !result.Status.AllInstancesKnown || reviews.reads != reads || gateway.renders.Load() != renders || gateway.creates.Load() != 2 {
		t.Fatal("duplicate submission re-read business facts or re-created an instance")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "9007199254740993") {
		t.Fatal("private body/proof escaped through batch diagnostics")
	}
	if _, err := store.UpdateApproval(context.Background(), "source", batch.Plans[0].Plan.ID, func(a *core.Attempt) error {
		a.Audit.RequestHash = strings.Repeat("a", 64)
		return nil
	}); err == nil {
		t.Fatal("a confirmed audit reference was mutable")
	}
}

func TestBatchSubmissionLostFirstResponseStopsUnsentGroupsAndResumesAfterUUIDQuery(t *testing.T) {
	service, batch, gateway, store, _, _ := batchFixture(t)
	gateway.failAt = 1
	result, err := service.Submit(context.Background(), batch.ID)
	if err == nil || gateway.creates.Load() != 1 || result.Status.Plans[0].Phase != "unknown" || result.Status.Plans[1].Phase != "reserved" {
		t.Fatal("lost response did not preserve partial and unsent states")
	}
	if _, err := service.Submit(context.Background(), batch.ID); !errors.Is(err, core.ErrReconcile) || gateway.creates.Load() != 1 {
		t.Fatal("rerun sent another request before UUID reconciliation")
	}
	result, err = app.ReconcileBatch(context.Background(), store, gateway, batch.ID)
	if err != nil || gateway.lookups.Load() != 1 || result.Status.Plans[0].Phase != "pending" || result.Status.Plans[1].Phase != "reserved" || result.Issue != "not_submitted" {
		t.Fatal("UUID query tried to create/query the unsent group")
	}
	result, err = service.Submit(context.Background(), batch.ID)
	if err != nil || !result.Status.AllInstancesKnown || gateway.creates.Load() != 2 {
		t.Fatal("restart did not resume exactly the remaining group")
	}
}

func TestBatchSubmissionHoldsWholeSelectionForChangedSourceOrConflictingMember(t *testing.T) {
	for _, scenario := range []string{"changed AI", "changed binding", "member occupied", "legacy attempt", "damaged audit"} {
		t.Run(scenario, func(t *testing.T) {
			service, batch, gateway, store, fields, reviews := batchFixture(t)
			expectedAttempts := 0
			switch scenario {
			case "changed AI":
				reviews.amount = "1"
			case "changed binding":
				previous := fields.onRead
				fields.onRead = func(call int, checks []app.InputCheck) []app.InputCheck {
					checks = previous(call, checks)
					checks[0].Row.SourceVersion = strings.Repeat("c", 64)
					return checks
				}
			case "member occupied":
				plan := batch.Plans[1].Plan
				row := plan.Rows[0]
				row.GroupValues = map[string]string{"project": "other group"}
				plans, err := core.BuildPlans(batchOptions(), []core.Row{row})
				if err != nil {
					t.Fatal(err)
				}
				store.BeginApproval(context.Background(), plans[0])
				expectedAttempts = 1
			case "legacy attempt":
				store.BeginApproval(context.Background(), batch.Plans[1].Plan)
				expectedAttempts = 1
			case "damaged audit":
				path, _ := store.ApprovalPreparationPath(batch.ID)
				os.WriteFile(path, nil, 0600)
			}
			result, err := service.Submit(context.Background(), batch.ID)
			attempts, readErr := store.ApprovalAttempts(context.Background(), "source")
			if err == nil || gateway.creates.Load() != 0 || readErr != nil || len(attempts) != expectedAttempts || result.Status.AllInstancesKnown {
				t.Fatal("invalid complete selection partially reserved or created another group")
			}
		})
	}
}

func TestBatchSubmissionRechecksUnsentGroupAfterEarlierCreation(t *testing.T) {
	service, batch, gateway, _, fields, _ := batchFixture(t)
	changed := false
	previous := fields.onRead
	fields.onRead = func(call int, checks []app.InputCheck) []app.InputCheck {
		checks = previous(call, checks)
		if changed {
			for n := range checks {
				if checks[n].RecordID == batch.Plans[1].Plan.Rows[0].RecordID {
					checks[n].Row.Fields["amount"] = core.Value{Kind: "money", Decimal: "1", Currency: "USD"}
				}
			}
		}
		return checks
	}
	gateway.after = func(core.Plan) { changed = true }
	result, err := service.Submit(context.Background(), batch.ID)
	if err == nil || gateway.creates.Load() != 1 || result.Status.Plans[0].Phase != "pending" || result.Status.Plans[1].Phase != "reserved" {
		t.Fatal("a changed later group was sent or the earlier instance was lost")
	}
}

func TestBatchConcurrentReservationAndClaimGiveExactlyOneSender(t *testing.T) {
	_, batch, _, store, _, _ := batchFixture(t)
	path, _ := store.ApprovalPreparationPath(batch.ID)
	root := filepath.Dir(path)
	var group sync.WaitGroup
	var claims atomic.Int64
	for n := 0; n < 12; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			other, err := state.OpenFiles(root)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := other.BeginApprovalBatch(context.Background(), batch.ID); err != nil && !errors.Is(err, core.ErrReconcile) {
				t.Error(err)
				return
			}
			_, claimed, err := other.ClaimApprovalSend(context.Background(), "source", batch.Plans[0].Plan.ID, batch.Plans[0].AuditReference(batch.ID))
			if err != nil {
				t.Error(err)
			} else if claimed {
				claims.Add(1)
			}
		}()
	}
	group.Wait()
	if claims.Load() != 1 {
		t.Fatal("more than one process acquired a native send intent")
	}
	status, err := app.ReadBatchStatus(context.Background(), store, batch.ID)
	if err != nil || status.Plans[0].Phase != "submitting" || status.Plans[1].Phase != "reserved" {
		t.Fatal("whole batch reservation or send intent lost")
	}
}

func TestAbandonmentReleasesOnlyUnsentReservationsAndPreservesUnknownInstance(t *testing.T) {
	service, batch, gateway, store, _, _ := batchFixture(t)
	gateway.failAt = 1
	if _, err := service.Submit(context.Background(), batch.ID); err == nil {
		t.Fatal("expected lost first response")
	}
	if _, err := store.AbandonApprovalReservations(context.Background(), batch.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := app.ReadBatchStatus(context.Background(), store, batch.ID)
	if status.Plans[0].Phase != "unknown" || status.Plans[1].Phase != "failed" || status.Plans[1].Failure.Code != "abandoned" || gateway.creates.Load() != 1 {
		t.Fatal("abandonment forgot a sent request or failed to release the unsent one")
	}
	for n, prepared := range batch.Plans {
		row := prepared.Plan.Rows[0]
		row.GroupValues = map[string]string{"project": "new group"}
		plans, _ := core.BuildPlans(batchOptions(), []core.Row{row})
		_, created, err := store.BeginApproval(context.Background(), plans[0])
		if n == 0 && !errors.Is(err, core.ErrMemberReserved) || n == 1 && (err != nil || !created) {
			t.Fatal("unknown group released or unsent group kept occupied")
		}
	}
}

type batchFaultStore struct {
	*state.Files
	claimBefore, claimAfter, updateFailure bool
}

func (s *batchFaultStore) ClaimApprovalSend(ctx context.Context, scope, id string, audit core.AuditReference) (core.Attempt, bool, error) {
	if s.claimBefore {
		return core.Attempt{}, false, errors.New("PRIVATE_SAVE_FAILURE")
	}
	attempt, claimed, err := s.Files.ClaimApprovalSend(ctx, scope, id, audit)
	if err == nil && claimed && s.claimAfter {
		return attempt, claimed, errors.New("PRIVATE_SAVE_FAILURE")
	}
	return attempt, claimed, err
}
func (s *batchFaultStore) UpdateApproval(ctx context.Context, scope, id string, update func(*core.Attempt) error) (core.Attempt, error) {
	if s.updateFailure {
		return core.Attempt{}, errors.New("PRIVATE_RESULT_SAVE_FAILURE")
	}
	return s.Files.UpdateApproval(ctx, scope, id, update)
}

func TestBatchSaveFailuresDoNotSendOrRepeatAnUncertainNativeRequest(t *testing.T) {
	for _, scenario := range []string{"before send intent", "after send intent", "after remote acceptance"} {
		t.Run(scenario, func(t *testing.T) {
			_, batch, gateway, store, _, _ := batchFixture(t)
			source, _, _ := batchSourceFixture(t)
			faults := &batchFaultStore{Files: store, claimBefore: scenario == "before send intent", claimAfter: scenario == "after send intent", updateFailure: scenario == "after remote acceptance"}
			service, _ := app.NewBatchService(source, gateway, faults, batchOptions())
			result, err := service.Submit(context.Background(), batch.ID)
			if err == nil || result.Status.AllInstancesKnown || result.Status.Plans[1].Phase != "reserved" {
				t.Fatal("failed save reported a complete batch or sent a later group")
			}
			if scenario == "before send intent" {
				if gateway.creates.Load() != 0 || result.Status.Plans[0].Phase != "reserved" {
					t.Fatal("native request sent without durable send intent")
				}
			} else {
				expected := int64(0)
				if scenario == "after remote acceptance" {
					expected = 1
				}
				if gateway.creates.Load() != expected || result.Status.Plans[0].Phase != "submitting" {
					t.Fatal("uncertain send/result save lost its intent")
				}
				if _, err := service.Submit(context.Background(), batch.ID); !errors.Is(err, core.ErrReconcile) || gateway.creates.Load() != expected {
					t.Fatal("uncertain persistence failure caused another POST")
				}
			}
		})
	}
}

func TestBatchCancellationAfterFirstAcceptancePreservesItsMappingAndUnsentReservation(t *testing.T) {
	service, batch, gateway, _, _, _ := batchFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway.after = func(core.Plan) { cancel() }
	result, err := service.Submit(ctx, batch.ID)
	if err == nil || gateway.creates.Load() != 1 || result.Status.Plans[0].Phase != "pending" || result.Status.Plans[1].Phase != "reserved" {
		t.Fatal("caller cancellation lost accepted result or sent another group")
	}
}

func TestDirectReconcilerDoesNotQueryReservedOrLocallyAbandonedUUID(t *testing.T) {
	_, batch, gateway, store, _, _ := batchFixture(t)
	if _, err := store.BeginApprovalBatch(context.Background(), batch.ID); err != nil {
		t.Fatal(err)
	}
	reconciler, _ := app.NewReconciler(gateway, store, "source")
	if _, err := reconciler.Reconcile(context.Background(), batch.Plans[0].Plan.ID); !errors.Is(err, core.ErrNotSubmitted) || gateway.lookups.Load() != 0 {
		t.Fatal("reserved UUID queried as a remote send")
	}
	store.AbandonApprovalReservations(context.Background(), batch.ID)
	if _, err := reconciler.Reconcile(context.Background(), batch.Plans[0].Plan.ID); !errors.Is(err, core.ErrNotSubmitted) || gateway.lookups.Load() != 0 {
		t.Fatal("locally abandoned UUID queried as a remote send")
	}
}
