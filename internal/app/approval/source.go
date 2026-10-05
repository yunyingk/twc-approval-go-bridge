package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

var ErrInputsNotReady = errors.New("approval source inputs are not ready")

// Input diagnostics contain stable semantic keys and codes, never source values.
type InputIssue struct {
	Input string `json:"input"`
	Code  string `json:"code"`
}
type InputCheck struct {
	RecordID     string            `json:"record_id"`
	Issues       []InputIssue      `json:"issues"`
	Row          core.Row          `json:"-"`
	ReviewFields map[string]string `json:"-"` // semantic -> decision/comment, filled only from current saved outcome
}
type FieldsSource interface {
	ApprovalSourceScope() string
	ReadApprovalInputs(context.Context, []string) ([]InputCheck, error)
}
type SourceInspection struct {
	Reviews []ReviewCheck `json:"records"`
	Inputs  []InputCheck  `json:"source_inputs"`
	rows    []core.Row
}

// Rows never returns a partially eligible selection. Missing current AI evidence
// or one unresolved form input holds the whole explicitly selected batch.
func (i SourceInspection) Rows() ([]core.Row, error) {
	if len(i.rows) == 0 || len(i.rows) != len(i.Inputs) {
		return nil, ErrInputsNotReady
	}
	return i.rows, nil
}

type PreparedSource struct {
	fields FieldsSource
	gate   *ReviewGate
}

func NewPreparedSource(fields FieldsSource, gate *ReviewGate) (*PreparedSource, error) {
	if fields == nil || gate == nil || fields.ApprovalSourceScope() != gate.scope {
		return nil, fmt.Errorf("approval typed fields and AI evidence require the same physical source")
	}
	return &PreparedSource{fields, gate}, nil
}
func (s *PreparedSource) ApprovalSourceScope() string { return s.fields.ApprovalSourceScope() }
func (s *PreparedSource) ReadApprovalRows(ctx context.Context, ids []string) ([]core.Row, error) {
	inspection, err := s.Inspect(ctx, ids)
	if err != nil {
		return nil, err
	}
	return inspection.Rows()
}

func checkedInputs(scope string, ids []string, checks []InputCheck) (map[string]InputCheck, error) {
	selected := map[string]bool{}
	for _, id := range ids {
		if id == "" || selected[id] {
			return nil, fmt.Errorf("approval inputs require unique selected records")
		}
		selected[id] = true
	}
	result := map[string]InputCheck{}
	for _, check := range checks {
		if !selected[check.RecordID] {
			return nil, fmt.Errorf("approval typed source did not return exactly the selected records")
		}
		delete(selected, check.RecordID)
		if len(check.Issues) == 0 && (check.Row.RecordID != check.RecordID || check.Row.SourceScope != scope || check.Row.SourceVersion == "" || len(check.Row.Fields)+len(check.ReviewFields) == 0 || check.Row.Review != (core.ReviewRef{})) {
			return nil, fmt.Errorf("approval typed source returned incomplete or foreign facts")
		}
		for semantic, field := range check.ReviewFields {
			if semantic == "" || (field != "decision" && field != "comment") {
				return nil, fmt.Errorf("approval review input binding is invalid")
			}
			if _, exists := check.Row.Fields[semantic]; exists {
				return nil, fmt.Errorf("approval review input cannot be replaced by source text")
			}
		}
		result[check.RecordID] = check
	}
	if len(selected) != 0 || len(result) == 0 {
		return nil, fmt.Errorf("approval typed source omitted selected records")
	}
	return result, nil
}

// Inspect brackets the current AI check with typed source reads. A form value,
// relation or grouping edit during preparation is reported as source_changed.
// This detects observed edits; remote Base reads are not an atomic transaction.
func (s *PreparedSource) Inspect(ctx context.Context, ids []string) (SourceInspection, error) {
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || id != strings.TrimSpace(id) || seen[id] {
			return SourceInspection{}, fmt.Errorf("approval requires unique explicit selected records")
		}
		seen[id] = true
	}
	if len(ids) == 0 {
		return SourceInspection{}, fmt.Errorf("approval requires selected records")
	}
	if s.fields.ApprovalSourceScope() != s.gate.scope {
		return SourceInspection{}, fmt.Errorf("approval typed source scope changed")
	}
	first, err := s.fields.ReadApprovalInputs(ctx, ids)
	if err != nil {
		return SourceInspection{}, err
	}
	byID, err := checkedInputs(s.gate.scope, ids, first)
	if err != nil {
		return SourceInspection{}, err
	}
	reviews, err := s.gate.Check(ctx, ids)
	if err != nil {
		return SourceInspection{}, err
	}
	eligible := []string{}
	for _, check := range reviews {
		if check.Evidence != nil && len(byID[check.RecordID].Issues) == 0 {
			eligible = append(eligible, check.RecordID)
		}
	}
	current := map[string]InputCheck{}
	if len(eligible) > 0 {
		second, err := s.fields.ReadApprovalInputs(ctx, eligible)
		if err != nil {
			return SourceInspection{}, err
		}
		current, err = checkedInputs(s.gate.scope, eligible, second)
		if err != nil {
			return SourceInspection{}, err
		}
	}
	inspection := SourceInspection{Reviews: reviews, Inputs: []InputCheck{}}
	for _, review := range reviews {
		input := byID[review.RecordID]
		if next, ok := current[review.RecordID]; ok {
			before, _ := json.Marshal(struct {
				Row          core.Row
				ReviewFields map[string]string
			}{input.Row, input.ReviewFields})
			after, _ := json.Marshal(struct {
				Row          core.Row
				ReviewFields map[string]string
			}{next.Row, next.ReviewFields})
			if len(next.Issues) > 0 || string(before) != string(after) {
				input.Issues = append(input.Issues, InputIssue{Input: "source", Code: "source_changed"})
			}
		}
		if len(input.Issues) == 0 && review.Evidence != nil {
			row := input.Row
			row.Fields = make(map[string]core.Value, len(input.Row.Fields)+len(input.ReviewFields))
			for semantic, value := range input.Row.Fields {
				row.Fields[semantic] = value
			}
			for semantic, field := range input.ReviewFields {
				value := review.Evidence.Outcome.Decision
				if field == "comment" {
					value = review.Evidence.Outcome.Comment
				}
				row.Fields[semantic] = core.Value{Kind: "text", Text: value}
			}
			row.Review = review.Evidence.Reference
			inspection.rows = append(inspection.rows, row)
		}
		inspection.Inputs = append(inspection.Inputs, input)
	}
	sort.Slice(inspection.Inputs, func(i, j int) bool { return inspection.Inputs[i].RecordID < inspection.Inputs[j].RecordID })
	return inspection, ctx.Err()
}
