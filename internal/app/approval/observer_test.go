package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

func approvalNotice(p core.Plan, id string) core.Notice {
	return core.Notice{ID: id, SourceScope: p.SourceScope, TargetScope: p.TargetScope, Template: p.Template, InstanceID: instance(p, "pending", true).ID, PlanID: p.ID}
}

func TestApprovalNoticeBeforeCreationResponseOnlyTriggersAuthoritativeUUIDLookup(t *testing.T) {
	ctx := context.Background()
	svc, source, gateway, store := fixture(t)
	plan := prepare(t, svc, "a", "b")
	observer, _ := app.NewObserver(plan.SourceScope, gateway, store, nil)
	notice := approvalNotice(plan, "event-before-response")
	gateway.create = func(p core.Plan) (core.Instance, error) {
		if err := observer.Enqueue(ctx, notice); err != nil {
			t.Fatal(err)
		}
		pending, err := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope)
		if err != nil || len(pending) != 1 || gateway.lookups.Load() != 0 {
			t.Fatal("event acknowledgement waited for a network lookup")
		}
		return core.Instance{}, errors.New("PRIVATE_LOST_RESPONSE")
	}
	if a, err := svc.Submit(ctx, plan); err == nil || a.Phase != "unknown" {
		t.Fatal("expected lost response")
	}
	reads := source.reads.Load()
	gateway.version = "different-current-template"
	gateway.lookup = func(p core.Plan) (core.Instance, error) {
		if p.ID != plan.ID || p.Template != plan.Template {
			t.Fatal("lookup lost frozen UUID/template")
		}
		return instance(p, "pending", true), nil
	}
	if err := observer.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := store.ReadApproval(ctx, plan.SourceScope, plan.ID)
	if a.Phase != "pending" || !a.Instance.Verified || len(a.History) != 1 || gateway.creates.Load() != 1 || source.reads.Load() != reads {
		t.Fatal("event decision or current source replaced authoritative lookup")
	}
	if pending, _ := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope); len(pending) != 0 {
		t.Fatal("confirmed lookup not acknowledged")
	}
	// A redelivery is a durable no-op, including after reopening the state store.
	reopened, _ := state.NewFiles(store.root)
	observer, _ = app.NewObserver(plan.SourceScope, gateway, reopened, nil)
	if err := observer.Enqueue(ctx, notice); err != nil {
		t.Fatal(err)
	}
	if err := observer.ProcessPending(ctx); err != nil || gateway.lookups.Load() != 1 {
		t.Fatal("duplicate reopened an acknowledged notice")
	}
	// Missed/offline events must still update completed and subsequently reverted instances.
	for _, status := range []string{"approved", "canceled", "reverted"} {
		gateway.lookup = func(p core.Plan) (core.Instance, error) { return instance(p, status, true), nil }
		if err := observer.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	a, _ = store.ReadApproval(ctx, plan.SourceScope, plan.ID)
	if a.Instance.Status != "reverted" || len(a.History) != 4 || gateway.creates.Load() != 1 {
		t.Fatal("offline terminal change was lost or recreated")
	}
	next := plan
	nextRows := append([]core.Row(nil), source.rows[:2]...)
	nextRows[0].Fields = map[string]core.Value{"amount": {Kind: "money", Decimal: "20", Currency: "USD"}}
	plans, err := core.BuildPlans(core.Options{SourceScope: plan.SourceScope, TargetScope: plan.TargetScope, Template: plan.Template, ConfigurationVersion: "changed", Submitter: plan.Submitter, Axes: []string{"project"}, AllowedDecisions: []string{"review"}}, nextRows)
	if err != nil {
		t.Fatal(err)
	}
	next = plans[0]
	if _, _, err := store.BeginApproval(ctx, next); !errors.Is(err, core.ErrMemberReserved) {
		t.Fatal("cancellation/reversion released financial occupancy")
	}
}

type faultNoticeStore struct {
	*testStore
	failQueue, failAck bool
}

func (s *faultNoticeStore) QueueApprovalNotice(ctx context.Context, n core.Notice) error {
	if s.failQueue {
		return errors.New("PRIVATE_QUEUE_ERROR")
	}
	return s.testStore.QueueApprovalNotice(ctx, n)
}
func (s *faultNoticeStore) FinishApprovalNotice(ctx context.Context, n core.Notice, f *core.Failure) error {
	if f == nil && s.failAck {
		return errors.New("PRIVATE_ACK_ERROR")
	}
	return s.testStore.FinishApprovalNotice(ctx, n, f)
}

func TestApprovalObservationRetainsQueryAndAcknowledgementFailuresAcrossRestart(t *testing.T) {
	ctx := context.Background()
	svc, _, gateway, store := fixture(t)
	plan := prepare(t, svc, "a")
	svc.Submit(ctx, plan)
	faults := &faultNoticeStore{testStore: store, failQueue: true}
	observer, _ := app.NewObserver(plan.SourceScope, gateway, faults, nil)
	n := approvalNotice(plan, "event-failure")
	if err := observer.Enqueue(ctx, n); err == nil || gateway.lookups.Load() != 0 {
		t.Fatal("failed queue was acknowledged or performed lookup")
	}
	faults.failQueue = false
	if err := observer.Enqueue(ctx, n); err != nil {
		t.Fatal(err)
	}
	gateway.lookup = func(core.Plan) (core.Instance, error) { return core.Instance{}, errors.New("PRIVATE_QUERY_ERROR") }
	if err := observer.ProcessPending(ctx); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("query failure disappeared or leaked")
	}
	pending, _ := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope)
	if len(pending) != 1 || pending[0].Failure == nil {
		t.Fatal("failed lookup not durably pending")
	}
	gateway.lookup = nil
	faults.failAck = true
	if err := observer.ProcessPending(ctx); err == nil {
		t.Fatal("acknowledgement save failure disappeared")
	}
	a, _ := store.ReadApproval(ctx, plan.SourceScope, plan.ID)
	if !a.Instance.Verified || a.Instance.Status != "approved" {
		t.Fatal("verified result was lost with acknowledgement")
	}
	reopened, _ := state.NewFiles(store.root)
	observer, _ = app.NewObserver(plan.SourceScope, gateway, reopened, nil)
	if err := observer.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, _ := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope); len(pending) != 0 || gateway.creates.Load() != 1 {
		t.Fatal("restart recreated instead of querying/acknowledging")
	}
	for _, path := range mustStatePaths(t, store.root) {
		raw, _ := os.ReadFile(path)
		info, _ := os.Stat(path)
		if strings.Contains(string(raw), "PRIVATE") || info.Mode().Perm() != 0600 {
			t.Fatal("private diagnostics or file permissions leaked")
		}
	}
}

func mustStatePaths(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestApprovalObserverScopesConflictAndCodeMismatchNeverRedirect(t *testing.T) {
	ctx := context.Background()
	svc, _, gateway, store := fixture(t)
	plan := prepare(t, svc, "a")
	svc.Submit(ctx, plan)
	observer, _ := app.NewObserver(plan.SourceScope, gateway, store, nil)
	n := approvalNotice(plan, "event-id")
	for _, kind := range []string{"unknown_uuid", "other_template", "other_instance", "other_scope", "other_target"} {
		bad := n
		switch kind {
		case "unknown_uuid":
			bad.PlanID = "missing"
		case "other_template":
			bad.Template = "other"
		case "other_instance":
			bad.InstanceID = "other"
		case "other_scope":
			bad.SourceScope = "other"
		case "other_target":
			bad.TargetScope = "other"
		}
		observer.Enqueue(ctx, bad)
	}
	if p, _ := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope); len(p) != 0 || gateway.lookups.Load() != 0 {
		t.Fatal("foreign notice persisted or queried")
	}
	// Concurrent redelivery binds once. An event ID cannot later bind another plan.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := observer.Enqueue(ctx, n); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p, _ := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope); len(p) != 1 {
		t.Fatal("duplicate notice not deduplicated")
	}
	other := prepare(t, svc, "c")
	svc.Submit(ctx, other)
	if err := observer.Enqueue(ctx, approvalNotice(other, n.ID)); !errors.Is(err, core.ErrConflict) {
		t.Fatal("event ID was redirected")
	}
	gateway.lookup = func(p core.Plan) (core.Instance, error) {
		v := instance(p, "approved", true)
		v.ID = "another-native-code"
		return v, nil
	}
	if err := observer.ProcessPending(ctx); err == nil {
		t.Fatal("event/native code disagreement accepted")
	}
	a, _ := store.ReadApproval(ctx, plan.SourceScope, plan.ID)
	if a.Instance.Verified || a.Instance.Status != "pending" {
		t.Fatal("conflicting query replaced mapped instance")
	}
	// Persisted triggers are decoded fail-closed if their binding is corrupted.
	paths := mustStatePaths(t, store.root)
	if len(paths) != 1 {
		t.Fatal("expected one source registry")
	}
	raw, _ := os.ReadFile(paths[0])
	var registry map[string]json.RawMessage
	json.Unmarshal(raw, &registry)
	var notices map[string]core.Notice
	json.Unmarshal(registry["notices"], &notices)
	for key, n := range notices {
		n.PlanID = "wrong-plan"
		notices[key] = n
	}
	registry["notices"], _ = json.Marshal(notices)
	raw, _ = json.Marshal(registry)
	os.WriteFile(paths[0], raw, 0600)
	if _, err := store.ApprovalAttempts(ctx, plan.SourceScope); err == nil {
		t.Fatal("corrupt persisted notice interpreted as a valid registry")
	}
}

func TestApprovalNoticeWithoutCreationUUIDMatchesOnlyKnownNativeCode(t *testing.T) {
	ctx := context.Background()
	svc, _, gateway, store := fixture(t)
	plan := prepare(t, svc, "a")
	observer, _ := app.NewObserver(plan.SourceScope, gateway, store, nil)
	store.BeginApproval(ctx, plan)
	n := approvalNotice(plan, "missing-uuid")
	n.PlanID = ""
	if err := observer.Enqueue(ctx, n); err != nil {
		t.Fatal(err)
	}
	if p, _ := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope); len(p) != 0 {
		t.Fatal("unmapped native code guessed a creation UUID")
	}
	if err := observer.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := observer.Enqueue(ctx, n); err != nil {
		t.Fatal(err)
	}
	if p, _ := store.PendingApprovalNotices(ctx, plan.SourceScope, plan.TargetScope); len(p) != 1 || p[0].PlanID != plan.ID {
		t.Fatal("known native code did not map original UUID")
	}
}

func TestApprovalObserverSkipsUnsentAndOtherApplicationAttempts(t *testing.T) {
	ctx := context.Background()
	_, batch, gateway, store, _, _ := batchFixture(t)
	if _, err := store.BeginApprovalBatch(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	observer, _ := app.NewObserver(batch.Plans[0].Plan.SourceScope, gateway, store, nil)
	if err := observer.Poll(ctx); err != nil || gateway.lookups.Load() != 0 {
		t.Fatal("reserved batch was queried")
	}
	if _, err := store.AbandonApprovalReservations(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	if err := observer.Poll(ctx); err != nil || gateway.lookups.Load() != 0 {
		t.Fatal("abandoned batch was queried")
	}
	if err := observer.Enqueue(ctx, approvalNotice(batch.Plans[0].Plan, "unsent")); err != nil {
		t.Fatal(err)
	}
	if p, _ := store.PendingApprovalNotices(ctx, "source", "target"); len(p) != 0 {
		t.Fatal("unsent plan accepted an external notice")
	}
	// A historical plan from another application is outside this observer.
	svc, _, otherGateway, otherStore := fixture(t)
	plan := prepare(t, svc, "a")
	svc.Submit(ctx, plan)
	foreign, _ := app.NewObserver(plan.SourceScope, &differentTargetLookup{gateway: otherGateway}, otherStore, nil)
	if err := foreign.Poll(ctx); err != nil || otherGateway.lookups.Load() != 0 {
		t.Fatal("poll fell back to another application")
	}
}

type differentTargetLookup struct{ *gateway }

func (*differentTargetLookup) TargetScope() string { return "other-application" }
