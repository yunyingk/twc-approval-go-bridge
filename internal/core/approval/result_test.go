package approval

import (
	"encoding/json"
	"strings"
	"testing"
)

func ms(value int64) *int64 { return &value }
func resultFixture(t *testing.T) (Plan, ResultSnapshot) {
	t.Helper()
	options, rows := fixture()
	plans, err := BuildPlans(options, rows)
	if err != nil {
		t.Fatal(err)
	}
	p := plans[0]
	actor := Identity{Scope: p.TargetScope, ID: "human-final"}
	return p, ResultSnapshot{Kind: "workflow", Instance: Instance{ID: "native", UUID: p.ID, TargetScope: p.TargetScope, Template: p.Template, SubmitterID: p.Submitter.ID, Status: "rejected", Verified: true}, StartedMS: ms(1000), CompletedMS: ms(4000),
		Tasks:    []ResultTask{{ID: "first", Actor: &Identity{p.TargetScope, "earlier"}, State: "approved", Kind: "all", StartedMS: ms(1000), CompletedMS: ms(2000)}, {ID: "last", Actor: &actor, State: "rejected", Kind: "sequential", StartedMS: ms(2000), CompletedMS: ms(4000)}},
		Comments: []ResultComment{{ID: "unrelated", Actor: &Identity{p.TargetScope, "other"}, Text: "unrelated comment", AtMS: ms(4100)}},
		Actions:  []ResultAction{{Kind: "reject", Actor: &actor, TaskID: "last", AtMS: ms(4000), Comment: "actual reject reason", SourceMetadata: json.RawMessage(`{"number":9007199254740993,"open_id":"human-final"}`)}, {Kind: "start", Actor: &p.Submitter, AtMS: ms(1000)}}}
}

func TestResultSnapshotCanonicalOrderingFreezeAndExactEvidenceRevision(t *testing.T) {
	plan, input := resultFixture(t)
	original, err := NewResultSnapshot(plan, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Tasks[0], input.Tasks[1] = input.Tasks[1], input.Tasks[0]
	input.Actions[0], input.Actions[1] = input.Actions[1], input.Actions[0]
	input.Actions[1].SourceMetadata = json.RawMessage(`{"open_id":"human-final","number":9007199254740993}`)
	again, err := NewResultSnapshot(plan, input)
	if err != nil || again.Revision != original.Revision || original.Validate(plan) != nil {
		t.Fatal("collection/metadata order changed result identity")
	}
	input.Tasks[0].Actor.ID = "mutated-caller"
	if original.Tasks[1].Actor.ID != "human-final" || again.Tasks[1].Actor.ID != "human-final" {
		t.Fatal("caller mutation changed frozen actor")
	}
	raw, _ := json.Marshal(original)
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("opaque metadata lost integer precision")
	}
	changed := original
	changed.Actions = append([]ResultAction(nil), original.Actions...)
	changed.Actions[1].Comment = "later explanation"
	next, err := NewResultSnapshot(plan, changed)
	if err != nil || next.Revision == original.Revision || changed.Validate(plan) == nil {
		t.Fatal("comment modification did not invalidate immutable evidence")
	}
}

func TestHumanDecisionUsesUniqueFinalTaskAndOnlyItsRejectReason(t *testing.T) {
	plan, input := resultFixture(t)
	result, _ := NewResultSnapshot(plan, input)
	decision, issue := result.HumanDecision(plan)
	if issue != "" || decision.Decision != "reject" || decision.TaskID != "last" || decision.Actor.ID != "human-final" || decision.CompletedMS != 4000 || decision.RejectComment != "actual reject reason" || decision.Revision != result.Revision {
		t.Fatalf("wrong final human evidence: %+v / %s", decision, issue)
	}
	for _, scenario := range []struct{ kind, want string }{
		{"automatic", "automatic_decision"}, {"actor_missing", "human_actor_missing"}, {"multiple_final", "terminal_actor_ambiguous"}, {"missing_completion", "completion_time_missing"}, {"missing_task_time", "task_time_incomplete"}, {"state_conflict", "terminal_task_conflict"}, {"unknown_task", "task_state_unknown"}, {"unknown_action", "workflow_action_unknown"}, {"status_only", "workflow_evidence_missing"}, {"withdrawn", "not_approve_or_reject"}, {"reason_conflict", "reject_reason_ambiguous"},
	} {
		t.Run(scenario.kind, func(t *testing.T) {
			p, s := resultFixture(t)
			switch scenario.kind {
			case "automatic":
				s.Tasks[1].Kind = "auto_reject"
				s.Tasks[1].Actor = nil
			case "actor_missing":
				s.Tasks[1].Actor = nil
			case "multiple_final":
				task := s.Tasks[1]
				task.ID = "parallel-last"
				task.Actor = &Identity{p.TargetScope, "parallel"}
				s.Tasks = append(s.Tasks, task)
			case "missing_completion":
				s.CompletedMS = nil
			case "missing_task_time":
				s.Tasks[0].CompletedMS = nil
			case "state_conflict":
				s.Tasks[1].State = "approved"
			case "unknown_task":
				s.Tasks[0].State = "unknown"
				s.Tasks[0].SourceState = "future"
			case "unknown_action":
				s.Actions = append(s.Actions, ResultAction{Kind: "unknown", SourceKind: "future"})
			case "status_only":
				s.Kind = "status_only"
			case "withdrawn":
				s.Instance.Status = "reverted"
			case "reason_conflict":
				action := s.Actions[0]
				action.Comment = "different same-time reason"
				s.Actions = append(s.Actions, action)
			}
			snapshot, err := NewResultSnapshot(p, s)
			if err != nil {
				t.Fatal(err)
			}
			if _, issue := snapshot.HumanDecision(p); issue != scenario.want {
				t.Fatalf("decision issue=%q want=%q", issue, scenario.want)
			}
		})
	}
}

func TestResultSnapshotRejectsForeignCorruptOrOversizedEvidence(t *testing.T) {
	for _, kind := range []string{"foreign_actor", "foreign_instance", "duplicate_task", "duplicate_comment", "negative_time", "completion_before_start", "malformed_metadata", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			p, s := resultFixture(t)
			switch kind {
			case "foreign_actor":
				s.Tasks[0].Actor.Scope = "another-app"
			case "foreign_instance":
				s.Instance.UUID = "another"
			case "duplicate_task":
				s.Tasks = append(s.Tasks, s.Tasks[0])
			case "duplicate_comment":
				s.Comments = append(s.Comments, s.Comments[0])
			case "negative_time":
				s.CompletedMS = ms(-1)
			case "completion_before_start":
				s.CompletedMS = ms(500)
			case "malformed_metadata":
				s.Actions[0].SourceMetadata = json.RawMessage(`{`)
			case "oversized":
				s.Comments[0].Text = strings.Repeat("x", 8<<20)
			}
			if _, err := NewResultSnapshot(p, s); err == nil {
				t.Fatal("invalid result evidence accepted")
			}
		})
	}
}
