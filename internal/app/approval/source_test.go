package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	review "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type typedFields struct {
	scope  string
	reads  [][]string
	onRead func(int, []app.InputCheck) []app.InputCheck
}

func (s *typedFields) ApprovalSourceScope() string { return s.scope }
func (s *typedFields) ReadApprovalInputs(_ context.Context, ids []string) ([]app.InputCheck, error) {
	s.reads = append(s.reads, append([]string(nil), ids...))
	checks := []app.InputCheck{}
	for _, id := range ids {
		checks = append(checks, app.InputCheck{RecordID: id, Issues: []app.InputIssue{}, ReviewFields: map[string]string{"ai_comment": "comment", "ai_decision": "decision"}, Row: core.Row{SourceScope: s.scope, SourceVersion: strings.Repeat("b", 64), RecordID: id, GroupValues: map[string]string{"project": "project-id"}, Fields: map[string]core.Value{"amount": {Kind: "money", Decimal: "9007199254740993.12", Currency: "USD"}}}})
	}
	if s.onRead != nil {
		checks = s.onRead(len(s.reads), checks)
	}
	return checks, nil
}
func preparedSourceFixture(t *testing.T) (*app.PreparedSource, *typedFields, *gateSource) {
	t.Helper()
	reviews := &gateSource{scope: "source", amount: "9007199254740993.12"}
	store := &gateStore{attempts: []review.Attempt{gateAttempt(t, reviews, "a", "pin", "completed", "review"), gateAttempt(t, reviews, "b", "pin", "completed", "review")}}
	gate, err := app.NewReviewGate(reviews, store, reviews.scope, "seal", []string{"review"})
	if err != nil {
		t.Fatal(err)
	}
	fields := &typedFields{scope: "source"}
	source, err := app.NewPreparedSource(fields, gate)
	if err != nil {
		t.Fatal(err)
	}
	return source, fields, reviews
}

func TestPreparedSourceCombinesOnlyCurrentSavedReviewWithTypedFields(t *testing.T) {
	source, fields, _ := preparedSourceFixture(t)
	inspection, err := source.Inspect(context.Background(), []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := inspection.Rows()
	if err != nil || len(rows) != 2 || rows[0].RecordID != "a" || rows[0].Fields["ai_comment"].Text != "PRIVATE_COMMENT" || rows[0].Fields["ai_decision"].Text != "review" || rows[0].Review.CurrentRevision != rows[0].Review.Revision || len(fields.reads) != 2 {
		t.Fatal("current outcome and typed form were not prepared together")
	}
	plans, err := core.BuildPlans(core.Options{SourceScope: "source", TargetScope: "target", Template: "template", ConfigurationVersion: "version", Submitter: core.Identity{Scope: "target", ID: "ou_submitter"}, Axes: []string{"project"}, AllowedDecisions: []string{"review"}}, rows)
	if err != nil || len(plans) != 1 || plans[0].Validate() != nil {
		t.Fatalf("prepared source cannot supply grouped approval: %v", err)
	}
	raw, _ := json.Marshal(inspection)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("private form values or AI comments leaked into inspection")
	}
}

func TestPreparedSourceHoldsWholeSelectionForStaleAIOrOneBlockedInput(t *testing.T) {
	for _, scenario := range []string{"stale AI", "upload required", "changed money", "changed grouping", "changed binding"} {
		t.Run(scenario, func(t *testing.T) {
			source, fields, reviews := preparedSourceFixture(t)
			if scenario == "stale AI" {
				reviews.amount = "1"
			} else {
				fields.onRead = func(call int, checks []app.InputCheck) []app.InputCheck {
					if scenario == "upload required" {
						for n := range checks {
							if checks[n].RecordID == "b" {
								checks[n].Issues = append(checks[n].Issues, app.InputIssue{Input: "files", Code: "approval_upload_required"})
							}
						}
					} else if call == 2 {
						switch scenario {
						case "changed money":
							checks[0].Row.Fields["amount"] = core.Value{Kind: "money", Decimal: "1", Currency: "USD"}
						case "changed grouping":
							checks[0].Row.GroupValues["project"] = "new-project"
						case "changed binding":
							checks[0].Row.SourceVersion = strings.Repeat("c", 64)
						}
					}
					return checks
				}
			}
			inspection, err := source.Inspect(context.Background(), []string{"a", "b"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inspection.Rows(); !errors.Is(err, app.ErrInputsNotReady) {
				t.Fatal("partially eligible selection escaped source gate")
			}
			if scenario == "stale AI" {
				if len(fields.reads) != 1 || inspection.Reviews[0].Code != "review_stale" {
					t.Fatal("stale AI was reused for native approval")
				}
			} else if scenario != "upload required" && inspection.Inputs[0].Issues[0].Code != "source_changed" {
				t.Fatal("preparation edit not reported")
			}
		})
	}
}

func TestPreparedSourceRejectsMalformedAdapterResultsAndInvalidSelection(t *testing.T) {
	for _, scenario := range []string{"foreign scope", "missing row", "duplicate", "unselected", "preloaded review", "AI text override"} {
		t.Run(scenario, func(t *testing.T) {
			source, fields, _ := preparedSourceFixture(t)
			fields.onRead = func(_ int, checks []app.InputCheck) []app.InputCheck {
				switch scenario {
				case "foreign scope":
					checks[0].Row.SourceScope = "other"
				case "missing row":
					return checks[:1]
				case "duplicate":
					checks[1] = checks[0]
				case "unselected":
					checks[0].RecordID = "foreign"
				case "preloaded review":
					checks[0].Row.Review.State = "completed"
				case "AI text override":
					checks[0].Row.Fields["ai_comment"] = core.Value{Kind: "text", Text: "unverified Base AI column"}
				}
				return checks
			}
			if _, err := source.Inspect(context.Background(), []string{"a", "b"}); err == nil {
				t.Fatal("malformed input source accepted")
			}
		})
	}
	source, fields, _ := preparedSourceFixture(t)
	for _, ids := range [][]string{{}, {"a", "a"}, {" a"}} {
		if _, err := source.Inspect(context.Background(), ids); err == nil {
			t.Fatal("invalid explicit selection accepted")
		}
	}
	if len(fields.reads) != 0 {
		t.Fatal("invalid selection reached source API")
	}
}
