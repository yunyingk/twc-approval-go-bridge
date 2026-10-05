package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ResultSnapshot keeps workflow evidence separate from source/AI facts. Missing
// timestamps or actors stay missing; zero timestamps mean explicitly unfinished.
// SourceKind/SourceState retain otherwise unknown provider labels for inspection,
// but cannot authorize a human decision. Attachment access URLs are not retained.
type ResultSnapshot struct {
	Version            int             `json:"version"`
	Kind               string          `json:"kind"` // workflow or status_only
	Revision           string          `json:"revision"`
	Instance           Instance        `json:"instance"`
	StartedMS          *int64          `json:"started_at_unix_ms,omitempty"`
	CompletedMS        *int64          `json:"completed_at_unix_ms,omitempty"`
	Tasks              []ResultTask    `json:"tasks"`
	Comments           []ResultComment `json:"comments"`
	Actions            []ResultAction  `json:"actions"`
	ModifiedInstanceID string          `json:"modified_instance_id,omitempty"`
	RevertedInstanceID string          `json:"reverted_instance_id,omitempty"`
}
type ResultFile struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Size *int64 `json:"size,omitempty"`
}
type ResultTask struct {
	ID           string    `json:"id"`
	Actor        *Identity `json:"actor,omitempty"`
	State        string    `json:"state"` // pending, approved, rejected, transferred, done, unknown
	Kind         string    `json:"kind"`  // all, any, sequential, auto_approve, auto_reject, unknown
	SourceState  string    `json:"source_state,omitempty"`
	SourceKind   string    `json:"source_kind,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	NodeName     string    `json:"node_name,omitempty"`
	CustomNodeID string    `json:"custom_node_id,omitempty"`
	StartedMS    *int64    `json:"started_at_unix_ms,omitempty"`
	CompletedMS  *int64    `json:"completed_at_unix_ms,omitempty"`
}
type ResultComment struct {
	ID    string       `json:"id"`
	Actor *Identity    `json:"actor,omitempty"`
	Text  string       `json:"text"`
	AtMS  *int64       `json:"at_unix_ms,omitempty"`
	Files []ResultFile `json:"files"`
}
type ResultAction struct {
	Kind           string          `json:"kind"`
	SourceKind     string          `json:"source_kind,omitempty"`
	Actor          *Identity       `json:"actor,omitempty"`
	AtMS           *int64          `json:"at_unix_ms,omitempty"`
	TaskID         string          `json:"task_id,omitempty"`
	NodeID         string          `json:"node_id,omitempty"`
	Comment        string          `json:"comment"`
	Recipients     []Identity      `json:"recipients"`
	Files          []ResultFile    `json:"files"`
	SourceMetadata json.RawMessage `json:"source_metadata,omitempty"`
}
type ResultObservation struct {
	Snapshot ResultSnapshot `json:"snapshot"`
	At       time.Time      `json:"at"`
}

func resultTimeValid(value *int64) bool { return value == nil || *value >= 0 }
func resultActorValid(actor *Identity, scope string) bool {
	return actor == nil || (actor.Scope == scope && validID(actor.ID))
}
func resultFilesValid(files []ResultFile) bool {
	for _, file := range files {
		if !resultTimeValid(file.Size) {
			return false
		}
	}
	return true
}

func (s ResultSnapshot) validateContents(plan Plan) error {
	if s.Version != 1 || plan.Validate() != nil {
		return ErrConflict
	}
	if s.Kind != "workflow" && s.Kind != "status_only" {
		return ErrConflict
	}
	if !s.Instance.Verified || s.Instance.Validate(plan) != nil || !resultTimeValid(s.StartedMS) || !resultTimeValid(s.CompletedMS) {
		return ErrConflict
	}
	if s.StartedMS != nil && s.CompletedMS != nil && *s.CompletedMS > 0 && *s.CompletedMS < *s.StartedMS {
		return ErrConflict
	}
	ids := map[string]bool{}
	for _, task := range s.Tasks {
		if !validID(task.ID) || ids[task.ID] || !resultActorValid(task.Actor, s.Instance.TargetScope) || !resultTimeValid(task.StartedMS) || !resultTimeValid(task.CompletedMS) {
			return ErrConflict
		}
		ids[task.ID] = true
		if task.StartedMS != nil && task.CompletedMS != nil && *task.CompletedMS > 0 && *task.CompletedMS < *task.StartedMS {
			return ErrConflict
		}
		switch task.State {
		case "pending", "approved", "rejected", "transferred", "done", "unknown":
		default:
			return ErrConflict
		}
		switch task.Kind {
		case "all", "any", "sequential", "auto_approve", "auto_reject", "unknown":
		default:
			return ErrConflict
		}
	}
	ids = map[string]bool{}
	for _, comment := range s.Comments {
		if !validID(comment.ID) || ids[comment.ID] || !resultActorValid(comment.Actor, s.Instance.TargetScope) || !resultTimeValid(comment.AtMS) || !resultFilesValid(comment.Files) {
			return ErrConflict
		}
		ids[comment.ID] = true
	}
	for _, action := range s.Actions {
		if !validID(action.Kind) || !resultActorValid(action.Actor, s.Instance.TargetScope) || !resultTimeValid(action.AtMS) || !resultFilesValid(action.Files) {
			return ErrConflict
		}
		if len(action.SourceMetadata) > 0 && !json.Valid(action.SourceMetadata) {
			return ErrConflict
		}
		for _, recipient := range action.Recipients {
			if !resultActorValid(&recipient, s.Instance.TargetScope) {
				return ErrConflict
			}
		}
	}
	return nil
}

// NewResultSnapshot freezes caller-owned data, sorts independent collections and
// fingerprints actual evidence, excluding local query/receipt timestamps.
func NewResultSnapshot(plan Plan, s ResultSnapshot) (ResultSnapshot, error) {
	if s.Version == 0 {
		s.Version = 1
	}
	s.Revision = ""
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > 8<<20 {
		return ResultSnapshot{}, fmt.Errorf("approval result exceeds evidence size limit")
	}
	var frozen ResultSnapshot
	if json.Unmarshal(raw, &frozen) != nil || frozen.validateContents(plan) != nil {
		return ResultSnapshot{}, ErrConflict
	}
	sort.Slice(frozen.Tasks, func(i, j int) bool { return frozen.Tasks[i].ID < frozen.Tasks[j].ID })
	sort.Slice(frozen.Comments, func(i, j int) bool { return frozen.Comments[i].ID < frozen.Comments[j].ID })
	for i := range frozen.Comments {
		sortResultFiles(frozen.Comments[i].Files)
	}
	for i := range frozen.Actions {
		sortResultFiles(frozen.Actions[i].Files)
		if len(frozen.Actions[i].SourceMetadata) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(frozen.Actions[i].SourceMetadata))
			decoder.UseNumber()
			var metadata any
			if decoder.Decode(&metadata) != nil {
				return ResultSnapshot{}, ErrConflict
			}
			frozen.Actions[i].SourceMetadata, err = json.Marshal(metadata)
			if err != nil {
				return ResultSnapshot{}, ErrConflict
			}
		}
		sort.Slice(frozen.Actions[i].Recipients, func(a, b int) bool { return frozen.Actions[i].Recipients[a].ID < frozen.Actions[i].Recipients[b].ID })
	}
	sort.SliceStable(frozen.Actions, func(i, j int) bool {
		a, b := frozen.Actions[i], frozen.Actions[j]
		if a.AtMS != nil && b.AtMS != nil && *a.AtMS != *b.AtMS {
			return *a.AtMS < *b.AtMS
		}
		if (a.AtMS == nil) != (b.AtMS == nil) {
			return a.AtMS == nil
		}
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return string(x) < string(y)
	})
	raw, err = json.Marshal(frozen)
	if err != nil {
		return ResultSnapshot{}, err
	}
	digest := sha256.Sum256(append([]byte("approval-result-v1:"), raw...))
	frozen.Revision = hex.EncodeToString(digest[:])
	return frozen, nil
}
func sortResultFiles(files []ResultFile) {
	sort.Slice(files, func(i, j int) bool {
		a, _ := json.Marshal(files[i])
		b, _ := json.Marshal(files[j])
		return string(a) < string(b)
	})
}
func (s ResultSnapshot) Validate(plan Plan) error {
	normalized, err := NewResultSnapshot(plan, s)
	if err != nil || normalized.Revision != s.Revision {
		return ErrConflict
	}
	a, _ := json.Marshal(s)
	b, _ := json.Marshal(normalized)
	if string(a) != string(b) {
		return ErrConflict
	}
	return nil
}

// HumanDecision is a uniquely evidenced last human task. It has no guessed
// display name/email; a destination must resolve that exact scoped actor.
type HumanDecision struct {
	Revision      string
	Decision      string
	Actor         Identity
	TaskID        string
	CompletedMS   int64
	RejectComment string
}

func (s ResultSnapshot) HumanDecision(plan Plan) (HumanDecision, string) {
	if s.Validate(plan) != nil {
		return HumanDecision{}, "result_invalid"
	}
	if s.Kind != "workflow" {
		return HumanDecision{}, "workflow_evidence_missing"
	}
	if s.Instance.Status != "approved" && s.Instance.Status != "rejected" {
		return HumanDecision{}, "not_approve_or_reject"
	}
	if s.CompletedMS == nil || *s.CompletedMS <= 0 {
		return HumanDecision{}, "completion_time_missing"
	}
	last := int64(0)
	var candidates []ResultTask
	for _, task := range s.Tasks {
		if task.State == "unknown" {
			return HumanDecision{}, "task_state_unknown"
		}
		if task.State != "approved" && task.State != "rejected" {
			continue
		}
		if task.CompletedMS == nil || *task.CompletedMS <= 0 || *task.CompletedMS > *s.CompletedMS {
			return HumanDecision{}, "task_time_incomplete"
		}
		if *task.CompletedMS > last {
			last = *task.CompletedMS
			candidates = nil
		}
		if *task.CompletedMS == last {
			candidates = append(candidates, task)
		}
	}
	for _, action := range s.Actions {
		if action.Kind == "unknown" {
			return HumanDecision{}, "workflow_action_unknown"
		}
	}
	if len(candidates) == 0 {
		return HumanDecision{}, "terminal_task_missing"
	}
	if len(candidates) != 1 {
		return HumanDecision{}, "terminal_actor_ambiguous"
	}
	task := candidates[0]
	if task.State != s.Instance.Status {
		return HumanDecision{}, "terminal_task_conflict"
	}
	if task.Kind == "auto_approve" || task.Kind == "auto_reject" {
		return HumanDecision{}, "automatic_decision"
	}
	if task.Kind == "unknown" || task.Actor == nil {
		return HumanDecision{}, "human_actor_missing"
	}
	decision := map[string]string{"approved": "approve", "rejected": "reject"}[task.State]
	result := HumanDecision{s.Revision, decision, *task.Actor, task.ID, *s.CompletedMS, ""}
	if decision == "reject" {
		latest := int64(-1)
		comments := map[string]bool{}
		for _, action := range s.Actions {
			if action.Kind != "reject" || action.TaskID != task.ID || action.Actor == nil || *action.Actor != *task.Actor || action.AtMS == nil || *action.AtMS > *s.CompletedMS {
				continue
			}
			if *action.AtMS > latest {
				latest = *action.AtMS
				comments = map[string]bool{}
			}
			if *action.AtMS == latest {
				comments[action.Comment] = true
			}
		}
		if len(comments) > 1 {
			return HumanDecision{}, "reject_reason_ambiguous"
		}
		for text := range comments {
			result.RejectComment = text
		}
	}
	return result, ""
}
