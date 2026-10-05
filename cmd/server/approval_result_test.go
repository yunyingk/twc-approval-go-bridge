package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type commandResultLookup struct{ calls int }

func (*commandResultLookup) TargetScope() string { return "feishu-app:bridge" }
func (*commandResultLookup) Lookup(context.Context, core.Plan) (core.Instance, error) {
	panic("rich result path must use one lookup")
}
func (g *commandResultLookup) LookupResult(_ context.Context, p core.Plan) (core.ResultSnapshot, error) {
	g.calls++
	end := int64(4000)
	return core.NewResultSnapshot(p, core.ResultSnapshot{Kind: "workflow", Instance: core.Instance{ID: "instance-" + p.ID, UUID: p.ID, TargetScope: p.TargetScope, Template: p.Template, SubmitterID: p.Submitter.ID, Status: "approved", Verified: true}, CompletedMS: &end,
		Tasks:    []core.ResultTask{{ID: "task", Actor: &core.Identity{Scope: p.TargetScope, ID: "PRIVATE_ACTOR"}, State: "approved", Kind: "all", CompletedMS: &end}},
		Comments: []core.ResultComment{{ID: "comment", Text: "PRIVATE_COMMENT", AtMS: &end}}, Actions: []core.ResultAction{{Kind: "approve", Comment: "PRIVATE_ACTION", AtMS: &end}}})
}

func TestApprovalStatusExposesResultRevisionAndCountsWithoutPrivateWorkflow(t *testing.T) {
	ctx := context.Background()
	cfg, service, batch, store, _ := commandBatchFixture(t)
	if _, err := service.Submit(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	lookup := &commandResultLookup{}
	result, err := app.ReconcileBatch(ctx, store, lookup, batch.ID)
	if err != nil || lookup.calls != 2 {
		t.Fatalf("batch did not use one authoritative rich lookup per plan: %v", err)
	}
	for _, plan := range result.Status.Plans {
		if plan.ResultRevision == "" || plan.ResultTasks != 1 || plan.ResultComments != 1 || plan.ResultActions != 1 || plan.HumanResultIssue != "" {
			t.Fatal("safe result summary was missing")
		}
	}
	var output bytes.Buffer
	if err := runApprovalStatus(ctx, cfg, batch.ID, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "PRIVATE") || !strings.Contains(output.String(), `"result_comments":1`) || !strings.Contains(output.String(), `"result_revision"`) {
		t.Fatal("public status exposed workflow evidence or omitted result summary")
	}
}
