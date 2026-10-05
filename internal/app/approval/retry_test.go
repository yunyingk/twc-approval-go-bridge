package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type rejectedApproval struct{}

func (rejectedApproval) Error() string           { return "PRIVATE_PERMISSION_ERROR" }
func (rejectedApproval) HTTPStatus() int         { return 400 }
func (rejectedApproval) RemoteErrorCode() string { return "99991672" }

func rejectFirstBatchRequest(g *batchGateway) {
	g.failAt, g.failErr = 1, errors.Join(core.ErrRejected, rejectedApproval{})
}

func rejectedFailure() *core.Failure {
	return &core.Failure{Phase: "creation", Code: "failed", HTTPStatus: 400, RemoteCode: "99991672", OccurredAt: time.Now().UTC()}
}

func TestExplicitRetryRetainsRejectedProofAcrossFailedLookupAndPreservesOriginalRequest(t *testing.T) {
	ctx := context.Background()
	service, batch, gateway, store, _, reviews := batchFixture(t)
	rejectFirstBatchRequest(gateway)
	result, err := service.Submit(ctx, batch.ID)
	if !errors.Is(err, core.ErrRejected) || result.Status.Plans[0].NoCreation.Kind != "rejected" || !result.Status.Plans[0].Retryable || result.Status.Plans[1].Phase != "reserved" {
		t.Fatal("explicit rejection did not retain retry proof and unsent group")
	}
	first := batch.Plans[0].Plan
	original, _ := store.ReadApproval(ctx, "source", first.ID)
	reads := reviews.reads
	if _, err := service.Submit(ctx, batch.ID); !errors.Is(err, core.ErrReconcile) || gateway.creates.Load() != 1 || reviews.reads != reads {
		t.Fatal("ordinary submit automatically retried a failed request")
	}
	gateway.lookup = func(core.Plan) (core.Instance, error) { return core.Instance{}, errors.New("PRIVATE_NOT_FOUND") }
	if _, err := app.ReconcileBatch(ctx, store, gateway, batch.ID); err == nil {
		t.Fatal("expected lookup failure")
	}
	failed, _ := store.ReadApproval(ctx, "source", first.ID)
	if failed.Failure.Phase != "reconciliation" || failed.RetryProof() == nil || *failed.NoCreation != *original.NoCreation {
		t.Fatal("lookup failure erased the original non-creation evidence")
	}
	if _, err := store.UpdateApproval(ctx, "source", first.ID, func(a *core.Attempt) error { a.NoCreation = nil; return nil }); err == nil {
		t.Fatal("original proof could be deleted by a diagnostic update")
	}
	result, err = service.Retry(ctx, batch.ID)
	if err != nil || !result.Status.AllInstancesKnown || gateway.creates.Load() != 3 || gateway.lookups.Load() != 1 {
		t.Fatalf("explicit retry did not send original rejected and remaining group: %v / %s", err, result.Issue)
	}
	saved, _ := store.ReadApproval(ctx, "source", first.ID)
	if !reflect.DeepEqual(saved.Plan, original.Plan) || *saved.Audit != *original.Audit || saved.CreatedAt != original.CreatedAt || saved.Run != 1 ||
		len(saved.ClosedRuns) != 1 || saved.ClosedRuns[0].Proof != *original.NoCreation || saved.ClosedRuns[0].LastFailure.Phase != "reconciliation" {
		t.Fatal("retry rewrote frozen request or dropped rejection/lookup history")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), saved.SendToken) || strings.Contains(string(encoded), "9007199254740993") {
		t.Fatal("retry diagnostics exposed private errors, send token or business body")
	}
	reads = reviews.reads
	if _, err := service.Retry(ctx, batch.ID); err != nil || gateway.creates.Load() != 3 || reviews.reads != reads {
		t.Fatal("known retry performed new source reads or creation")
	}
	for _, mutate := range []func(*core.Attempt){
		func(a *core.Attempt) { a.ClosedRuns[0].Proof.Failure.RemoteCode = "1" },
		func(a *core.Attempt) { a.ClosedRuns = nil },
		func(a *core.Attempt) { a.Run++ },
		func(a *core.Attempt) { a.SendToken = strings.Repeat("a", 64) },
	} {
		if _, err := store.UpdateApproval(ctx, "source", first.ID, func(a *core.Attempt) error { mutate(a); return nil }); err == nil {
			t.Fatal("ordinary update rewrote retry history or run ownership")
		}
	}
}

func TestRetryNeedsAnExistingAttemptAndCanRestoreLocalAbandonment(t *testing.T) {
	ctx := context.Background()
	service, batch, gateway, store, _, reviews := batchFixture(t)
	reads := reviews.reads
	if _, err := service.Retry(ctx, batch.ID); !errors.Is(err, core.ErrNotSubmitted) || reviews.reads != reads || gateway.creates.Load() != 0 {
		t.Fatal("retry created an entirely unsubmitted batch")
	}
	if _, err := store.BeginApprovalBatch(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AbandonApprovalReservations(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	result, err := service.Retry(ctx, batch.ID)
	if err != nil || !result.Status.AllInstancesKnown || gateway.creates.Load() != 2 || gateway.lookups.Load() != 0 {
		t.Fatal("local abandonment required a fake query or could not explicitly resume")
	}
	for _, status := range result.Status.Plans {
		if status.Run != 1 || len(status.ClosedRuns) != 1 || status.ClosedRuns[0].Proof.Kind != "abandoned" {
			t.Fatal("unsent abandonment was lost when retrying")
		}
	}
}

func TestConfirmedUUIDWinsOverEarlierRejectionAndDoesNotRepeatCreation(t *testing.T) {
	ctx := context.Background()
	service, batch, gateway, store, _, _ := batchFixture(t)
	rejectFirstBatchRequest(gateway)
	if _, err := service.Submit(ctx, batch.ID); !errors.Is(err, core.ErrRejected) {
		t.Fatal("expected initial rejection")
	}
	first := batch.Plans[0].Plan
	before, _ := store.ReadApproval(ctx, "source", first.ID)
	if _, err := app.ReconcileBatch(ctx, store, gateway, batch.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := store.ReadApproval(ctx, "source", first.ID)
	if after.Instance == nil || !after.Instance.Verified || after.RetryProof() != nil || *after.NoCreation != *before.NoCreation {
		t.Fatal("confirmed UUID lost prior evidence or remained retryable")
	}
	result, err := service.Retry(ctx, batch.ID)
	if err != nil || !result.Status.AllInstancesKnown || gateway.creates.Load() != 2 || result.Status.Plans[0].Run != 0 {
		t.Fatal("UUID lookup confirmed instance was recreated instead of resuming later group")
	}
}

func TestOwnerProofRequiresExactRunAuditAndTokenAndCannotReleaseUnknownOrConfirmed(t *testing.T) {
	ctx := context.Background()
	for _, phase := range []string{"unknown", "confirmed"} {
		t.Run(phase, func(t *testing.T) {
			_, batch, gateway, store, _, _ := batchFixture(t)
			store.BeginApprovalBatch(ctx, batch.ID)
			plan := batch.Plans[0].Plan
			audit := batch.Plans[0].AuditReference(batch.ID)
			token := strings.Repeat("b", 64)
			if _, claimed, err := store.ClaimApprovalSendWithToken(ctx, "source", plan.ID, audit, token); err != nil || !claimed {
				t.Fatal("could not claim test intent")
			}
			wrongAudit := audit
			wrongAudit.RequestHash = strings.Repeat("a", 64)
			for _, check := range []struct {
				audit core.AuditReference
				run   uint64
				token string
			}{{audit, 0, strings.Repeat("c", 64)}, {wrongAudit, 0, token}, {audit, 1, token}} {
				if _, err := store.ProveApprovalNotSent(ctx, "source", plan.ID, check.audit, check.run, check.token); !errors.Is(err, core.ErrConflict) {
					t.Fatal("foreign proof could release someone else's send")
				}
			}
			if _, err := store.ProveApprovalNotSent(ctx, "source", plan.ID, audit, 0, token); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ProveApprovalNotSent(ctx, "source", plan.ID, audit, 0, token); err != nil {
				t.Fatal("duplicate own proof was not idempotent")
			}
			expected := map[string]uint64{}
			for _, p := range batch.Plans {
				expected[p.Plan.ID] = 0
			}
			if _, err := store.RetryApprovalBatch(ctx, batch.ID, expected); err != nil {
				t.Fatal(err)
			}
			newToken := strings.Repeat("d", 64)
			if _, claimed, err := store.ClaimApprovalSendWithToken(ctx, "source", plan.ID, audit, newToken); err != nil || !claimed {
				t.Fatal("retry intent not claimed")
			}
			for _, check := range []struct {
				run   uint64
				token string
			}{{0, token}, {0, newToken}, {1, token}} {
				if _, err := store.ProveApprovalNotSent(ctx, "source", plan.ID, audit, check.run, check.token); !errors.Is(err, core.ErrConflict) {
					t.Fatal("stale proof released a newer run")
				}
			}
			if phase == "unknown" {
				store.UpdateApproval(ctx, "source", plan.ID, func(a *core.Attempt) error { a.Phase = "unknown"; return nil })
			} else {
				reconciler, _ := app.NewReconciler(gateway, store, "source")
				if _, err := reconciler.Reconcile(ctx, plan.ID); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := store.ReadApproval(ctx, "source", plan.ID)
			if _, err := store.ProveApprovalNotSent(ctx, "source", plan.ID, audit, 1, newToken); err == nil {
				t.Fatal("unknown/confirmed run could be forgotten with its own token")
			}
			after, _ := store.ReadApproval(ctx, "source", plan.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refused proof modified saved run")
			}
		})
	}
}

type savedCreationUpdate struct {
	*state.Files
	first func(*core.Attempt) error
}

func (s *savedCreationUpdate) UpdateApproval(ctx context.Context, scope, id string, update func(*core.Attempt) error) (core.Attempt, error) {
	if s.first == nil {
		s.first = update
	}
	return s.Files.UpdateApproval(ctx, scope, id, update)
}

func TestLateCreationAndLookupUpdatesCannotOverwriteNewRetryRun(t *testing.T) {
	ctx := context.Background()
	_, batch, gateway, store, _, _ := batchFixture(t)
	source, _, _ := batchSourceFixture(t)
	recorded := &savedCreationUpdate{Files: store}
	service, _ := app.NewBatchService(source, gateway, recorded, batchOptions())
	rejectFirstBatchRequest(gateway)
	if _, err := service.Submit(ctx, batch.ID); !errors.Is(err, core.ErrRejected) {
		t.Fatal("expected rejection")
	}
	first := batch.Plans[0].Plan
	expected := map[string]uint64{}
	for _, p := range batch.Plans {
		expected[p.Plan.ID] = 0
	}
	gateway.lookup = func(core.Plan) (core.Instance, error) {
		if _, err := store.RetryApprovalBatch(ctx, batch.ID, expected); err != nil {
			t.Fatal(err)
		}
		return core.Instance{}, errors.New("PRIVATE_OLD_LOOKUP_FAILURE")
	}
	reconciler, _ := app.NewReconciler(gateway, store, "source")
	if _, err := reconciler.Reconcile(ctx, first.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatal("old lookup wrote into reopened run")
	}
	result, err := service.Submit(ctx, batch.ID)
	if err != nil || !result.Status.AllInstancesKnown {
		t.Fatal("reopened batch did not resume")
	}
	before, _ := store.ReadApproval(ctx, "source", first.ID)
	if _, err := store.UpdateApproval(ctx, "source", first.ID, recorded.first); !errors.Is(err, core.ErrConflict) {
		t.Fatal("old creation result was allowed into a later retry run")
	}
	after, _ := store.ReadApproval(ctx, "source", first.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("late original failure overwrote current instance/history")
	}
}

func TestRetryHoldsUnknownChangedFactsOccupiedMembersAndLegacyAmbiguity(t *testing.T) {
	for _, scenario := range []string{"unknown", "changed AI", "member reacquired", "legacy lost proof", "different batch"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			service, batch, gateway, store, _, reviews := batchFixture(t)
			gateway.failAt = 1
			if scenario != "unknown" {
				rejectFirstBatchRequest(gateway)
			}
			if _, err := service.Submit(ctx, batch.ID); err == nil {
				t.Fatal("expected first creation failure")
			}
			first := batch.Plans[0].Plan
			switch scenario {
			case "changed AI":
				reviews.amount = "changed"
			case "member reacquired":
				row := first.Rows[0]
				row.GroupValues = map[string]string{"project": "another plan"}
				plans, _ := core.BuildPlans(batchOptions(), []core.Row{row})
				if _, created, err := store.BeginApproval(ctx, plans[0]); err != nil || !created {
					t.Fatal("could not acquire released rejected member")
				}
			case "legacy lost proof":
				// A complete old registry remains readable, but a generic failed
				// phase plus lookup error cannot invent its missing rejection proof.
				other, _ := state.NewFiles(t.TempDir())
				other.SaveApprovalPreparation(ctx, batch)
				other.BeginApprovalBatch(ctx, batch.ID)
				other.ClaimApprovalSend(ctx, "source", first.ID, batch.Plans[0].AuditReference(batch.ID))
				other.UpdateApproval(ctx, "source", first.ID, func(a *core.Attempt) error {
					a.Phase, a.Failure = "failed", &core.Failure{Phase: "creation", Code: "failed"}
					return nil
				})
				other.UpdateApproval(ctx, "source", first.ID, func(a *core.Attempt) error {
					a.Failure = &core.Failure{Phase: "reconciliation", Code: "lookup_failed"}
					return nil
				})
				store = other
				source, _, _ := batchSourceFixture(t)
				service, _ = app.NewBatchService(source, gateway, store, batchOptions())
			case "different batch":
				subset, err := core.NewPreparedBatch([]core.PreparedPlan{batch.Plans[0]}, []core.ReviewProof{batch.Reviews[0]})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.SaveApprovalPreparation(ctx, subset); err != nil {
					t.Fatal(err)
				}
				batch = subset
			}
			before, _ := store.ApprovalAttempts(ctx, "source")
			result, err := service.Retry(ctx, batch.ID)
			after, _ := store.ApprovalAttempts(ctx, "source")
			if err == nil || gateway.creates.Load() != 1 || !reflect.DeepEqual(before, after) || result.Status.AllInstancesKnown {
				t.Fatal("unsafe retry sent, reset history or partially acquired selection")
			}
		})
	}
}

func TestConcurrentExplicitRetryReopensOnceAndOldRunCannotBeReopenedAgain(t *testing.T) {
	ctx := context.Background()
	service, batch, gateway, store, _, _ := batchFixture(t)
	rejectFirstBatchRequest(gateway)
	service.Submit(ctx, batch.ID)
	path, _ := store.ApprovalPreparationPath(batch.ID)
	expected := map[string]uint64{}
	for _, prepared := range batch.Plans {
		expected[prepared.Plan.ID] = 0
	}
	var group sync.WaitGroup
	var reopened, sent atomic.Int64
	for n := 0; n < 12; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			other, _ := state.OpenFiles(filepath.Dir(path))
			if _, err := other.RetryApprovalBatch(ctx, batch.ID, expected); err != nil {
				if !errors.Is(err, core.ErrReconcile) {
					t.Error(err)
				}
				return
			}
			reopened.Add(1)
			if _, claimed, err := other.ClaimApprovalSend(ctx, "source", batch.Plans[0].Plan.ID, batch.Plans[0].AuditReference(batch.ID)); err != nil {
				t.Error(err)
			} else if claimed {
				sent.Add(1)
			}
		}()
	}
	group.Wait()
	if reopened.Load() != 1 || sent.Load() != 1 {
		t.Fatal("concurrent explicit requests authorized more than one retry run")
	}
	first := batch.Plans[0].Plan
	_, err := store.UpdateApproval(ctx, "source", first.ID, func(a *core.Attempt) error {
		a.Phase, a.Failure = "failed", rejectedFailure()
		a.NoCreation = &core.NoCreationProof{Kind: "rejected", Failure: *a.Failure}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryApprovalBatch(ctx, batch.ID, expected); !errors.Is(err, core.ErrReconcile) {
		t.Fatal("old retry observation reopened a later failed run")
	}
}
