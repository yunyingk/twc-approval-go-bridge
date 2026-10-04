package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type source struct {
	rows  []core.Row
	reads atomic.Int64
}

func (*source) ApprovalSourceScope() string { return "source" }
func (s *source) ReadApprovalRows(_ context.Context, ids []string) ([]core.Row, error) {
	s.reads.Add(1)
	rows := []core.Row{}
	for _, id := range ids {
		for _, row := range s.rows {
			if row.RecordID == id {
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

type gateway struct {
	version string
	creates atomic.Int64
	lookups atomic.Int64
	create  func(core.Plan) (core.Instance, error)
	lookup  func(core.Plan) (core.Instance, error)
}

func (*gateway) TargetScope() string { return "target" }
func (g *gateway) Describe(context.Context) (app.Target, error) {
	return app.Target{Scope: "target", Template: "template", ConfigurationVersion: g.version}, nil
}
func (*gateway) ValidatePlan(context.Context, core.Plan) error { return nil }
func (g *gateway) Create(_ context.Context, plan core.Plan) (core.Instance, error) {
	g.creates.Add(1)
	if g.create != nil {
		return g.create(plan)
	}
	return instance(plan, "pending", false), nil
}
func (g *gateway) Lookup(_ context.Context, plan core.Plan) (core.Instance, error) {
	g.lookups.Add(1)
	if g.lookup != nil {
		return g.lookup(plan)
	}
	return instance(plan, "approved", true), nil
}
func instance(plan core.Plan, status string, verified bool) core.Instance {
	return core.Instance{ID: "instance-" + plan.ID, UUID: plan.ID, TargetScope: plan.TargetScope, Template: plan.Template, SubmitterID: plan.Submitter.ID, Status: status, Verified: verified}
}

type testStore struct {
	*state.Files
	root string
}

func fixture(t *testing.T) (*app.Service, *source, *gateway, *testStore) {
	t.Helper()
	revision := strings.Repeat("a", 64)
	s := &source{}
	for _, id := range []string{"a", "b", "c"} {
		s.rows = append(s.rows, core.Row{SourceScope: "source", RecordID: id, GroupValues: map[string]string{"project": "project"},
			Review: core.ReviewRef{DocumentID: "source:" + id + ":v:" + revision, Revision: revision, CurrentRevision: revision, State: "completed", Decision: "review"},
			Fields: map[string]core.Value{"amount": {Kind: "money", Decimal: "10", Currency: "USD"}}})
	}
	g := &gateway{version: "version"}
	root := t.TempDir()
	files, err := state.NewFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	store := &testStore{files, root}
	svc, err := app.New(s, g, store, app.Options{SourceScope: "source", Submitter: core.Identity{Scope: "target", ID: "submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, s, g, store
}
func prepare(t *testing.T, svc *app.Service, ids ...string) core.Plan {
	t.Helper()
	plans, err := svc.Prepare(context.Background(), ids)
	if err != nil || len(plans) != 1 {
		t.Fatalf("prepare selected group: %v", err)
	}
	return plans[0]
}

func TestUnknownCreationPersistsMappingAndRecoversByUUIDAfterRestart(t *testing.T) {
	svc, s, g, store := fixture(t)
	ctx := context.Background()
	plan := prepare(t, svc, "a", "b")
	g.create = func(p core.Plan) (core.Instance, error) {
		saved, err := store.ReadApproval(ctx, p.SourceScope, p.ID)
		if err != nil || saved.Phase != "submitting" || len(saved.Plan.Rows) != 2 {
			t.Fatal("creation preceded durable member/source-version mapping")
		}
		return core.Instance{}, errors.New("PRIVATE_CREATION_BODY")
	}
	a, err := svc.Submit(ctx, plan)
	if err == nil || a.Phase != "unknown" || a.Failure == nil || a.Instance != nil {
		t.Fatal("lost response did not retain unknown attempt")
	}
	encoded, _ := json.Marshal(a)
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("upstream failure body entered saved state")
	}
	if _, err := svc.Submit(ctx, plan); !errors.Is(err, core.ErrReconcile) || g.creates.Load() != 1 {
		t.Fatal("unknown submission was automatically repeated")
	}
	// A new template configuration and changed source are irrelevant to UUID lookup.
	g.version = "new-configuration"
	s.rows[0].Fields["amount"] = core.Value{Kind: "money", Decimal: "20", Currency: "USD"}
	reconciler, err := app.NewReconciler(g, store, "source")
	if err != nil {
		t.Fatal(err)
	}
	reads := s.reads.Load()
	a, err = reconciler.Reconcile(ctx, plan.ID)
	if err != nil || a.Phase != "finished" || !a.Instance.Verified || a.Instance.Status != "approved" || a.Failure != nil || g.creates.Load() != 1 || s.reads.Load() != reads {
		t.Fatal("UUID reconciliation depended on new facts or repeated creation")
	}
	if _, err := reconciler.Submit(ctx, plan); err == nil {
		t.Fatal("delivery-only reconciler could submit")
	}
	// Approval does not release financial occupancy just because the AI/version changed.
	next := prepare(t, svc, "a", "b")
	if _, err := svc.Submit(ctx, next); !errors.Is(err, core.ErrMemberReserved) || g.creates.Load() != 1 {
		t.Fatal("approved details entered another group")
	}
}

func TestChangedFactsAndConfigurationStopBeforeReservationOrCreate(t *testing.T) {
	for _, change := range []string{"facts", "configuration", "review"} {
		t.Run(change, func(t *testing.T) {
			svc, s, g, store := fixture(t)
			plan := prepare(t, svc, "a")
			switch change {
			case "facts":
				s.rows[0].Fields["amount"] = core.Value{Kind: "money", Decimal: "11", Currency: "USD"}
			case "configuration":
				g.version = "changed"
			case "review":
				s.rows[0].Review.CurrentRevision = strings.Repeat("b", 64)
			}
			if _, err := svc.Submit(context.Background(), plan); err == nil || g.creates.Load() != 0 {
				t.Fatal("stale plan reached creation")
			}
			attempts, err := store.ApprovalAttempts(context.Background(), "source")
			if err != nil || len(attempts) != 0 {
				t.Fatal("invalid plan reserved source members")
			}
		})
	}
}

func TestConcurrentSamePlanCreatesOnceAndOverlappingGroupCannotClaim(t *testing.T) {
	svc, _, g, store := fixture(t)
	plan := prepare(t, svc, "a", "b")
	var group sync.WaitGroup
	errorsCh := make(chan error, 12)
	for range 12 {
		group.Add(1)
		go func() { defer group.Done(); _, err := svc.Submit(context.Background(), plan); errorsCh <- err }()
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil && !errors.Is(err, core.ErrReconcile) {
			t.Fatal(err)
		}
	}
	if g.creates.Load() != 1 {
		t.Fatal("concurrent attempts created multiple approvals")
	}
	other := prepare(t, svc, "a", "c")
	second, err := state.OpenFiles(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.BeginApproval(context.Background(), other); !errors.Is(err, core.ErrMemberReserved) {
		t.Fatal("another process could claim an overlapping group")
	}
	// A rejected all-or-nothing group must not reserve its otherwise free member.
	free := prepare(t, svc, "c")
	if _, err := svc.Submit(context.Background(), free); err != nil || g.creates.Load() != 2 {
		t.Fatal("failed overlap left a partial member reservation")
	}
}

func TestEarlyLookupDominatesCreateFailureAndKeepsIdentityMapping(t *testing.T) {
	svc, _, g, store := fixture(t)
	ctx := context.Background()
	plan := prepare(t, svc, "a")
	g.create = func(p core.Plan) (core.Instance, error) {
		if _, err := svc.Reconcile(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
		return core.Instance{}, errors.New("lost response after lookup")
	}
	a, err := svc.Submit(ctx, plan)
	if err != nil || a.Phase != "finished" || a.Instance.Status != "approved" {
		t.Fatal("create failure overwrote confirmed human result")
	}
	g.lookup = func(p core.Plan) (core.Instance, error) { return instance(p, "pending", true), nil }
	if _, err := svc.Reconcile(ctx, plan.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatal("final human decision regressed to pending")
	}
	a, _ = store.ReadApproval(ctx, "source", plan.ID)
	if a.Instance.Status != "approved" {
		t.Fatal("old query overwrote confirmed result")
	}
}

type failingStore struct {
	app.Store
	beginError, updateError bool
}

func (s *failingStore) BeginApproval(ctx context.Context, plan core.Plan) (core.Attempt, bool, error) {
	if s.beginError {
		return core.Attempt{}, false, errors.New("state unavailable")
	}
	return s.Store.BeginApproval(ctx, plan)
}
func (s *failingStore) UpdateApproval(ctx context.Context, scope, id string, update func(*core.Attempt) error) (core.Attempt, error) {
	if s.updateError {
		return core.Attempt{}, errors.New("result write unavailable")
	}
	return s.Store.UpdateApproval(ctx, scope, id, update)
}

func TestPersistenceFailuresStopCreationOrRecoverAcceptedInstance(t *testing.T) {
	for _, stage := range []string{"before_create", "after_create"} {
		t.Run(stage, func(t *testing.T) {
			original, s, g, store := fixture(t)
			plan := prepare(t, original, "a", "b")
			fault := &failingStore{Store: store, beginError: stage == "before_create", updateError: stage == "after_create"}
			svc, err := app.New(s, g, fault, app.Options{SourceScope: "source", Submitter: core.Identity{Scope: "target", ID: "submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Submit(context.Background(), plan); err == nil {
				t.Fatal("persistence failure was acknowledged as saved")
			}
			if stage == "before_create" {
				if g.creates.Load() != 0 {
					t.Fatal("creation preceded successful reservation")
				}
				return
			}
			// Simulate restart after the provider accepted but local save failed.
			reopened, err := state.OpenFiles(store.root)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := reopened.ReadApproval(context.Background(), "source", plan.ID)
			if err != nil || pending.Phase != "submitting" || pending.Instance != nil {
				t.Fatalf("pre-call mapping lost after result write failure: %v", err)
			}
			reconciler, _ := app.NewReconciler(g, reopened, "source")
			got, err := reconciler.Reconcile(context.Background(), plan.ID)
			if err != nil || got.Instance == nil || !got.Instance.Verified || g.creates.Load() != 1 {
				t.Fatalf("accepted instance could not be recovered without re-creation: %v", err)
			}
		})
	}
}

func TestOverlappingPlansRaceAcrossIndependentStores(t *testing.T) {
	svc, _, _, store := fixture(t)
	other, err := state.OpenFiles(store.root)
	if err != nil {
		t.Fatal(err)
	}
	plans := []core.Plan{prepare(t, svc, "a", "b"), prepare(t, svc, "b", "c")}
	stores := []app.Store{store, other}
	type result struct {
		i       int
		created bool
		err     error
	}
	ready, results := make(chan struct{}), make(chan result, 2)
	for i := range plans {
		go func(i int) {
			<-ready
			_, created, err := stores[i].BeginApproval(context.Background(), plans[i])
			results <- result{i, created, err}
		}(i)
	}
	close(ready)
	winner := -1
	for range plans {
		r := <-results
		if r.err == nil && r.created {
			if winner != -1 {
				t.Fatal("both overlapping batches reserved the same detail")
			}
			winner = r.i
		} else if !errors.Is(r.err, core.ErrMemberReserved) {
			t.Fatalf("unexpected claim result: %v", r.err)
		}
	}
	if winner == -1 {
		t.Fatal("no batch acquired the available details")
	}
	freeID := "a"
	if winner == 0 {
		freeID = "c"
	}
	free := prepare(t, svc, freeID)
	if _, created, err := store.BeginApproval(context.Background(), free); err != nil || !created {
		t.Fatal("losing batch left a partial reservation on its exclusive member")
	}
}

func TestUnknownLookupCannotReleaseMembersOrRewriteFrozenMapping(t *testing.T) {
	svc, _, g, store := fixture(t)
	plan := prepare(t, svc, "a", "b")
	g.create = func(core.Plan) (core.Instance, error) { return core.Instance{}, errors.New("lost response") }
	if _, err := svc.Submit(context.Background(), plan); err == nil {
		t.Fatal("expected unknown creation")
	}
	g.lookup = func(core.Plan) (core.Instance, error) { return core.Instance{}, errors.New("not found") }
	if a, err := svc.Reconcile(context.Background(), plan.ID); err == nil || a.Phase != "unknown" {
		t.Fatal("negative lookup changed unknown state")
	}
	if _, err := svc.Submit(context.Background(), prepare(t, svc, "b", "c")); !errors.Is(err, core.ErrMemberReserved) {
		t.Fatal("negative lookup released uncertain members")
	}
	for _, change := range []string{"release", "plan"} {
		if _, err := store.UpdateApproval(context.Background(), "source", plan.ID, func(a *core.Attempt) error {
			if change == "release" {
				a.Phase = "failed"
			} else {
				a.Plan.Rows[0].RecordID = "foreign-record"
			}
			return nil
		}); err == nil {
			t.Fatal("unsafe saved mapping edit accepted")
		}
	}
	g.lookup = func(p core.Plan) (core.Instance, error) { return instance(p, "approved", true), nil }
	if _, err := svc.Reconcile(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateApproval(context.Background(), "source", plan.ID, func(a *core.Attempt) error {
		a.Instance.ID = "another-instance"
		return nil
	}); !errors.Is(err, core.ErrConflict) {
		t.Fatal("confirmed mapping replaced in place")
	}
}

func TestExplicitRejectionAllowsNewPlanButNeverRepeatsOriginal(t *testing.T) {
	svc, s, g, _ := fixture(t)
	plan := prepare(t, svc, "a")
	g.create = func(core.Plan) (core.Instance, error) { return core.Instance{}, core.ErrRejected }
	if a, err := svc.Submit(context.Background(), plan); !errors.Is(err, core.ErrRejected) || a.Phase != "failed" {
		t.Fatal("definitive rejection was not saved")
	}
	if _, err := svc.Submit(context.Background(), plan); !errors.Is(err, core.ErrReconcile) || g.creates.Load() != 1 {
		t.Fatal("same rejected plan was automatically re-created")
	}
	s.rows[0].Fields["amount"] = core.Value{Kind: "money", Decimal: "20", Currency: "USD"}
	g.create = nil
	if _, err := svc.Submit(context.Background(), prepare(t, svc, "a")); err != nil || g.creates.Load() != 2 {
		t.Fatal("proven rejection retained external occupancy")
	}
}

func TestCallerCancellationAfterAcceptanceStillPersistsInstance(t *testing.T) {
	svc, _, g, store := fixture(t)
	plan := prepare(t, svc, "a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.create = func(p core.Plan) (core.Instance, error) { cancel(); return instance(p, "pending", false), nil }
	if _, err := svc.Submit(ctx, plan); err != nil {
		t.Fatal(err)
	}
	saved, err := store.ReadApproval(context.Background(), "source", plan.ID)
	if err != nil || saved.Instance == nil || saved.Phase != "pending" {
		t.Fatal("caller cancellation dropped accepted instance mapping")
	}
}
