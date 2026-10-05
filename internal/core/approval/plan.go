// Package approval defines grouped human-approval facts and immutable source
// references. Platform widgets, SDK requests and identity translation stay in adapters.
package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
	corereview "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

var (
	ErrReviewNotCurrent = errors.New("human approval requires a completed current AI review")
	ErrMemberReserved   = errors.New("source detail already belongs to another approval plan")
	ErrReconcile        = errors.New("approval attempt requires reconciliation before another submission")
	ErrRejected         = errors.New("approval creation was explicitly rejected")
	ErrPlanChanged      = errors.New("approval plan no longer matches current business facts or configuration")
	ErrUnknown          = errors.New("unknown approval plan")
	ErrConflict         = errors.New("approval instance conflicts with saved mapping")
	ErrNotSubmitted     = errors.New("approval reservation has not been sent")
)

// Identity scopes distinguish application/tenant identities, even when their
// display names match. Files also carry the scope that issued their reference.
type Identity struct {
	Scope string `json:"scope"`
	ID    string `json:"id"`
}
type Value struct {
	Kind       string     `json:"kind"` // text, date, number, money, people, files
	Text       string     `json:"text,omitempty"`
	Decimal    string     `json:"decimal,omitempty"`
	Currency   string     `json:"currency,omitempty"`
	References []Identity `json:"references,omitempty"`
	Artifacts  []string   `json:"artifacts,omitempty"` // Durable upload request IDs for file provenance; absent on older plans.
}
type ReviewRef struct {
	DocumentID      string `json:"document_id"`
	Revision        string `json:"revision"`
	CurrentRevision string `json:"current_revision"`
	State           string `json:"state"`
	Decision        string `json:"decision"`
}
type Row struct {
	SourceScope   string            `json:"source_scope"`
	SourceVersion string            `json:"source_version,omitempty"` // Pins typed source bindings; absent on older plans.
	RecordID      string            `json:"record_id"`
	GroupValues   map[string]string `json:"group_values"`
	Review        ReviewRef         `json:"review"`
	Fields        map[string]Value  `json:"fields"`
}
type Options struct {
	SourceScope, TargetScope, Template, ConfigurationVersion, DepartmentID string
	Submitter                                                              Identity
	Axes, AllowedDecisions                                                 []string
}
type Plan struct {
	ID                   string            `json:"id"`
	Revision             string            `json:"revision"`
	SourceScope          string            `json:"source_scope"`
	TargetScope          string            `json:"target_scope"`
	Template             string            `json:"template"`
	ConfigurationVersion string            `json:"configuration_version"`
	DepartmentID         string            `json:"department_id,omitempty"`
	Submitter            Identity          `json:"submitter"`
	Axes                 []string          `json:"axes"`
	AllowedDecisions     []string          `json:"allowed_decisions"`
	Group                map[string]string `json:"group"`
	Rows                 []Row             `json:"rows"`
}

func validID(value string) bool { return value != "" && value == strings.TrimSpace(value) }

func normalizeValue(value Value, targetScope string) (Value, error) {
	if len(value.Artifacts) > 0 {
		if value.Kind != "files" || len(value.Artifacts) != len(value.References) {
			return Value{}, fmt.Errorf("file provenance must match issued references")
		}
		value.Artifacts = append([]string(nil), value.Artifacts...)
		sort.Strings(value.Artifacts)
		for n, id := range value.Artifacts {
			hash, err := hex.DecodeString(id)
			if err != nil || len(hash) != sha256.Size || strings.ToLower(id) != id || (n > 0 && value.Artifacts[n-1] == id) {
				return Value{}, fmt.Errorf("file provenance requires unique upload identities")
			}
		}
	}
	switch value.Kind {
	case "text", "date":
		if value.Decimal != "" || value.Currency != "" || len(value.References) != 0 {
			return Value{}, fmt.Errorf("text/date value contains incompatible data")
		}
		if value.Kind == "date" {
			if _, err := time.Parse(time.RFC3339, value.Text); err != nil {
				return Value{}, fmt.Errorf("date requires an explicit timestamp and offset")
			}
		}
	case "number", "money":
		number, ok := invoice.Decimal(value.Decimal)
		if !ok || value.Text != "" || len(value.References) != 0 ||
			(value.Kind == "money" && !validID(value.Currency)) || (value.Kind == "number" && value.Currency != "") {
			return Value{}, fmt.Errorf("numeric value requires exact decimal data and explicit money currency")
		}
		value.Decimal = string(number)
	case "people", "files":
		if value.Text != "" || value.Decimal != "" || value.Currency != "" || len(value.References) == 0 {
			return Value{}, fmt.Errorf("reference value requires scoped identities or files")
		}
		value.References = append([]Identity(nil), value.References...)
		seen := map[string]bool{}
		for _, ref := range value.References {
			if ref.Scope != targetScope || !validID(ref.ID) || seen[ref.ID] {
				return Value{}, fmt.Errorf("identity/file reference is duplicated or belongs to another target scope")
			}
			seen[ref.ID] = true
		}
		sort.Slice(value.References, func(i, j int) bool { return value.References[i].ID < value.References[j].ID })
	default:
		return Value{}, fmt.Errorf("unsupported business value kind")
	}
	return value, nil
}

func normalizeOptions(options Options) (Options, error) {
	for _, value := range []string{options.SourceScope, options.TargetScope, options.Template, options.ConfigurationVersion, options.Submitter.ID} {
		if !validID(value) {
			return Options{}, fmt.Errorf("approval source, target, template, configuration and submitter are required")
		}
	}
	if options.Submitter.Scope != options.TargetScope || options.DepartmentID != strings.TrimSpace(options.DepartmentID) {
		return Options{}, fmt.Errorf("approval submitter or department does not match the target")
	}
	options.Axes = append([]string{}, options.Axes...)
	options.AllowedDecisions = append([]string{}, options.AllowedDecisions...)
	sort.Strings(options.Axes)
	sort.Strings(options.AllowedDecisions)
	for _, values := range [][]string{options.Axes, options.AllowedDecisions} {
		for n, value := range values {
			if !validID(value) || (n > 0 && values[n-1] == value) {
				return Options{}, fmt.Errorf("approval grouping axes and decisions must be explicit and unique")
			}
		}
	}
	if len(options.AllowedDecisions) == 0 {
		return Options{}, fmt.Errorf("approval requires an explicit allowed AI decision policy")
	}
	for _, value := range options.AllowedDecisions {
		if err := (corereview.Outcome{Decision: value}).Validate(); err != nil {
			return Options{}, fmt.Errorf("invalid allowed AI decision")
		}
	}
	return options, nil
}

// BuildPlans groups exactly the supplied rows. An empty axis list explicitly
// groups a selected batch; date bucketing and physical field lookup belong to sources.
func BuildPlans(options Options, rows []Row) ([]Plan, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("approval requires source rows")
	}
	groups := map[string]*Plan{}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.SourceScope != options.SourceScope || !validID(row.RecordID) || seen[row.RecordID] {
			return nil, fmt.Errorf("approval source rows are duplicated or belong to another scope")
		}
		seen[row.RecordID] = true
		if row.SourceVersion != "" {
			version, err := hex.DecodeString(row.SourceVersion)
			if err != nil || len(version) != sha256.Size {
				return nil, fmt.Errorf("approval row source binding version is invalid")
			}
		}
		ref := row.Review
		revision, err := hex.DecodeString(ref.Revision)
		if err != nil || len(revision) != sha256.Size || ref.State != "completed" || ref.CurrentRevision != ref.Revision ||
			ref.DocumentID != options.SourceScope+":"+row.RecordID+":v:"+ref.Revision || !contains(options.AllowedDecisions, ref.Decision) {
			return nil, fmt.Errorf("record %s: %w", row.RecordID, ErrReviewNotCurrent)
		}
		group := map[string]string{}
		for _, axis := range options.Axes {
			value := row.GroupValues[axis]
			if !validID(value) {
				return nil, fmt.Errorf("record %s lacks an explicit grouping value", row.RecordID)
			}
			group[axis] = value
		}
		row.GroupValues = group
		fields := make(map[string]Value, len(row.Fields))
		if len(row.Fields) == 0 {
			return nil, fmt.Errorf("approval row requires business fields")
		}
		for semantic, value := range row.Fields {
			if !validID(semantic) {
				return nil, fmt.Errorf("approval business field requires a stable semantic key")
			}
			value, err := normalizeValue(value, options.TargetScope)
			if err != nil {
				return nil, fmt.Errorf("record %s field %s: %w", row.RecordID, semantic, err)
			}
			fields[semantic] = value
		}
		row.Fields = fields
		key, _ := json.Marshal(group)
		plan := groups[string(key)]
		if plan == nil {
			plan = &Plan{SourceScope: options.SourceScope, TargetScope: options.TargetScope, Template: options.Template, ConfigurationVersion: options.ConfigurationVersion,
				DepartmentID: options.DepartmentID, Submitter: options.Submitter, Axes: options.Axes, AllowedDecisions: options.AllowedDecisions, Group: group}
			groups[string(key)] = plan
		}
		plan.Rows = append(plan.Rows, row)
	}
	plans := make([]Plan, 0, len(groups))
	for _, plan := range groups {
		sort.Slice(plan.Rows, func(i, j int) bool { return plan.Rows[i].RecordID < plan.Rows[j].RecordID })
		plan.Revision, plan.ID, err = planIdentity(*plan)
		if err != nil {
			return nil, err
		}
		plans = append(plans, *plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].ID < plans[j].ID })
	return plans, nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func planIdentity(plan Plan) (string, string, error) {
	plan.ID, plan.Revision = "", ""
	body, err := json.Marshal(plan)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(body)
	revision := hex.EncodeToString(digest[:])
	// The immutable content hash supplies a stable RFC 9562 UUIDv8. Providers
	// receive the same ID on recovery; this is not a random retry nonce.
	digest[6] = (digest[6] & 0x0f) | 0x80
	digest[8] = (digest[8] & 0x3f) | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", digest[:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
	return revision, id, nil
}

func (p Plan) Validate() error {
	options := Options{SourceScope: p.SourceScope, TargetScope: p.TargetScope, Template: p.Template, ConfigurationVersion: p.ConfigurationVersion,
		DepartmentID: p.DepartmentID, Submitter: p.Submitter, Axes: p.Axes, AllowedDecisions: p.AllowedDecisions}
	plans, err := BuildPlans(options, p.Rows)
	if err != nil {
		return err
	}
	if len(plans) != 1 || plans[0].ID != p.ID || plans[0].Revision != p.Revision {
		return fmt.Errorf("approval plan identity does not match its frozen contents")
	}
	// Group metadata itself must also match; it cannot be edited independently.
	a, _ := json.Marshal(plans[0])
	b, _ := json.Marshal(p)
	if string(a) != string(b) {
		return fmt.Errorf("approval plan is not canonical")
	}
	return nil
}
