package approval

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func fixture() (Options, []Row) {
	options := Options{SourceScope: "scope", TargetScope: "target", Template: "template", ConfigurationVersion: "configuration", Submitter: Identity{"target", "submitter"}, Axes: []string{"project", "period"}, AllowedDecisions: []string{"review", "approve"}}
	revision := strings.Repeat("a", 64)
	rows := []Row{}
	for _, id := range []string{"b", "a"} {
		rows = append(rows, Row{SourceScope: "scope", RecordID: id, GroupValues: map[string]string{"project": "project-id", "period": "2026-10"},
			Review: ReviewRef{DocumentID: "scope:" + id + ":v:" + revision, Revision: revision, CurrentRevision: revision, State: "completed", Decision: "review"},
			Fields: map[string]Value{"amount": {Kind: "money", Decimal: "9007199254740993.12", Currency: "USD"}, "people": {Kind: "people", References: []Identity{{"target", "person-b"}, {"target", "person-a"}}}}})
	}
	return options, rows
}

func TestPlanStableAcrossReadOrderAndTracksEveryBusinessInput(t *testing.T) {
	options, rows := fixture()
	plans, err := BuildPlans(options, rows)
	if err != nil || len(plans) != 1 || plans[0].Rows[0].RecordID != "a" {
		t.Fatal("group did not preserve sorted source references")
	}
	original := plans[0]
	rows[0], rows[1] = rows[1], rows[0]
	options.Axes[0], options.Axes[1] = options.Axes[1], options.Axes[0]
	rows[0].GroupValues["unused_display_name"] = "renamed project"
	value := rows[0].Fields["people"]
	value.References[0], value.References[1] = value.References[1], value.References[0]
	rows[0].Fields["people"] = value
	again, err := BuildPlans(options, rows)
	if err != nil || !reflect.DeepEqual(original, again[0]) || original.Validate() != nil {
		t.Fatal("input ordering or unused display data changed immutable identity")
	}
	if original.Rows[0].Fields["amount"].Decimal != "9007199254740993.12" || len(original.ID) != 36 {
		t.Fatal("amount precision or stable UUID format lost")
	}
	for _, input := range []string{"business amount", "group", "template", "submitter", "configuration", "review revision", "source binding"} {
		t.Run(input, func(t *testing.T) {
			opts, changed := fixture()
			switch input {
			case "business amount":
				changed[0].Fields["amount"] = Value{Kind: "money", Decimal: "20", Currency: "USD"}
			case "group":
				for n := range changed {
					changed[n].GroupValues["period"] = "2026-11"
				}
			case "template":
				opts.Template = "another-template"
			case "submitter":
				opts.Submitter.ID = "another-submitter"
			case "configuration":
				opts.ConfigurationVersion = "another-version"
			case "source binding":
				changed[0].SourceVersion = strings.Repeat("c", 64)
			case "review revision":
				ref := &changed[0].Review
				ref.Revision, ref.CurrentRevision = strings.Repeat("b", 64), strings.Repeat("b", 64)
				ref.DocumentID = "scope:" + changed[0].RecordID + ":v:" + ref.Revision
			}
			next, err := BuildPlans(opts, changed)
			if err != nil || next[0].ID == original.ID {
				t.Fatal("material input did not change plan identity")
			}
		})
	}
	original.Rows[0].Fields["amount"] = Value{Kind: "money", Decimal: "0", Currency: "USD"}
	if original.Validate() == nil {
		t.Fatal("tampered frozen plan validated")
	}
}

func TestPlanRejectsStaleReviewAndForeignIdentityBeforeGrouping(t *testing.T) {
	for _, scenario := range []string{"stale", "pending", "foreign record review", "foreign source", "foreign person", "foreign file", "missing currency", "ambiguous decimal", "missing group", "duplicate row", "excluded decision", "invalid source version"} {
		t.Run(scenario, func(t *testing.T) {
			opts, rows := fixture()
			switch scenario {
			case "stale":
				rows[0].Review.CurrentRevision = strings.Repeat("b", 64)
			case "pending":
				rows[0].Review.State = "pending"
			case "foreign record review":
				rows[0].Review.DocumentID = rows[1].Review.DocumentID
			case "foreign source":
				rows[0].SourceScope = "another-source"
			case "invalid source version":
				rows[0].SourceVersion = "unverified-binding"
			case "foreign person", "foreign file":
				kind := "people"
				if scenario == "foreign file" {
					kind = "files"
				}
				rows[0].Fields["people"] = Value{Kind: kind, References: []Identity{{"another-application", "same-display-name"}}}
			case "missing currency":
				rows[0].Fields["amount"] = Value{Kind: "money", Decimal: "10"}
			case "ambiguous decimal":
				rows[0].Fields["amount"] = Value{Kind: "money", Decimal: "1,23", Currency: "USD"}
			case "missing group":
				delete(rows[0].GroupValues, "period")
			case "duplicate row":
				rows[0] = rows[1]
			case "excluded decision":
				rows[0].Review.Decision = "reject"
			}
			_, err := BuildPlans(opts, rows)
			if err == nil {
				t.Fatal("unsafe approval plan prepared")
			}
			if (scenario == "stale" || scenario == "pending" || scenario == "foreign record review" || scenario == "excluded decision") && !errors.Is(err, ErrReviewNotCurrent) {
				t.Fatal("review gate classification lost")
			}
		})
	}
}
