package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type resultGateway struct {
	*gateway
	result func(core.Plan) (core.ResultSnapshot, error)
}

func (g *resultGateway) LookupResult(_ context.Context, p core.Plan) (core.ResultSnapshot, error) {
	return g.result(p)
}
func workflowResult(p core.Plan, text string) (core.ResultSnapshot, error) {
	start, end := int64(1000), int64(4000)
	return core.NewResultSnapshot(p, core.ResultSnapshot{Kind: "workflow", Instance: instance(p, "approved", true), StartedMS: &start, CompletedMS: &end,
		Tasks:    []core.ResultTask{{ID: "human-task", Actor: &core.Identity{Scope: p.TargetScope, ID: "original-actor"}, State: "approved", Kind: "any", StartedMS: &start, CompletedMS: &end}},
		Comments: []core.ResultComment{{ID: "comment", Actor: &core.Identity{Scope: p.TargetScope, ID: "original-actor"}, Text: text, AtMS: &end}}})
}

func TestResultObservationPersistsMetadataChangesWithoutChangingStateOrRecreating(t *testing.T) {
	ctx := context.Background()
	svc, _, g, store := fixture(t)
	p := prepare(t, svc, "a")
	svc.Submit(ctx, p)
	text := "PRIVATE_COMMENT"
	rich := &resultGateway{gateway: g, result: func(plan core.Plan) (core.ResultSnapshot, error) { return workflowResult(plan, text) }}
	reconciler, _ := app.NewReconciler(rich, store, p.SourceScope)
	a, err := reconciler.Reconcile(ctx, p.ID)
	if err != nil || len(a.Results) != 1 || a.CurrentResult() == nil {
		t.Fatal("rich lookup did not persist actual evidence")
	}
	first := a.Results[0]
	a, err = reconciler.Reconcile(ctx, p.ID)
	if err != nil || len(a.Results) != 1 || a.Results[0].At != first.At || !a.ResultObservedAt.Equal(a.LastObservedAt) {
		t.Fatal("identical polling duplicated result history or lost freshness")
	}
	text = "PRIVATE_UPDATED_COMMENT"
	a, err = reconciler.Reconcile(ctx, p.ID)
	if err != nil || len(a.Results) != 2 || len(a.History) != 1 || a.Results[0].Snapshot.Revision != first.Snapshot.Revision || a.Results[1].Snapshot.Revision == first.Snapshot.Revision || g.creates.Load() != 1 {
		t.Fatal("metadata update was lost, changed status history or recreated approval")
	}
	// Historical/custom status-only queries cannot make old comments current.
	statusOnly, _ := app.NewReconciler(g, store, p.SourceScope)
	a, err = statusOnly.Reconcile(ctx, p.ID)
	if err != nil || a.CurrentResult() != nil || len(a.Results) != 2 {
		t.Fatal("status-only gateway relabeled old workflow evidence as current")
	}
	a, err = reconciler.Reconcile(ctx, p.ID)
	if err != nil || a.CurrentResult() == nil || len(a.Results) != 2 {
		t.Fatal("rich poll did not restore current evidence")
	}
	for _, mutate := range []func(*core.Attempt){
		func(a *core.Attempt) { a.Results = nil },
		func(a *core.Attempt) { a.Results[0].Snapshot.Comments[0].Text = "rewritten-history" },
		func(a *core.Attempt) { a.ResultObservedAt = a.ResultObservedAt.Add(-1) },
	} {
		if _, err := store.UpdateApproval(ctx, p.SourceScope, p.ID, func(a *core.Attempt) error { mutate(a); return nil }); err == nil {
			t.Fatal("result history/freshness was mutable")
		}
	}
	reopened, _ := state.NewFiles(store.root)
	a, err = reopened.ReadApproval(ctx, p.SourceScope, p.ID)
	if err != nil || len(a.Results) != 2 || a.Results[0].Snapshot.Comments[0].Text != "PRIVATE_COMMENT" || a.CurrentResult() == nil {
		t.Fatal("restart lost immutable result history")
	}
	rich.result = func(core.Plan) (core.ResultSnapshot, error) {
		return core.ResultSnapshot{}, errors.New("PRIVATE_QUERY_ERROR")
	}
	a, err = reconciler.Reconcile(ctx, p.ID)
	if err == nil || a.CurrentResult() != nil || len(a.Results) != 2 {
		t.Fatal("failed query lost result or exposed it as current")
	}
	raw, _ := json.Marshal(a.Failure)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("query error leaked into diagnostic state")
	}
}

func TestConcurrentResultQueryKeepsNewerEvidenceAndRejectsWrongEventCode(t *testing.T) {
	ctx := context.Background()
	svc, _, g, store := fixture(t)
	p := prepare(t, svc, "a")
	svc.Submit(ctx, p)
	entered, release := make(chan struct{}), make(chan struct{})
	old := &resultGateway{gateway: g, result: func(plan core.Plan) (core.ResultSnapshot, error) {
		close(entered)
		<-release
		return workflowResult(plan, "old")
	}}
	newer := &resultGateway{gateway: g, result: func(plan core.Plan) (core.ResultSnapshot, error) { return workflowResult(plan, "new") }}
	oldReconciler, _ := app.NewReconciler(old, store, p.SourceScope)
	newReconciler, _ := app.NewReconciler(newer, store, p.SourceScope)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := oldReconciler.Reconcile(ctx, p.ID); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	if _, err := newReconciler.Reconcile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	a, _ := store.ReadApproval(ctx, p.SourceScope, p.ID)
	if len(a.Results) != 1 || a.CurrentResult().Comments[0].Text != "new" {
		t.Fatal("late older query replaced newer workflow evidence")
	}
	if _, err := newReconciler.ReconcileExpected(ctx, p.ID, "foreign-code"); !errors.Is(err, core.ErrConflict) {
		t.Fatal("event/native code mismatch saved rich evidence")
	}
	a, _ = store.ReadApproval(ctx, p.SourceScope, p.ID)
	if len(a.Results) != 1 || a.CurrentResult() != nil {
		t.Fatal("mismatch did not hold current decision")
	}
}
