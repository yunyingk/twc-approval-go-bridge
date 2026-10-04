package approval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

type ControlSelector struct {
	ID       string `json:"id,omitempty"`
	CustomID string `json:"custom_id,omitempty"`
}

// DetailFormBinding maps business semantics to the current template. ID and
// custom_id are alternatives, never display-name or positional selectors.
type DetailFormBinding struct {
	Detail ControlSelector            `json:"detail"`
	Fields map[string]ControlSelector `json:"fields"`
}

type FormValue struct {
	Text      string   `json:"text,omitempty"`
	Decimal   string   `json:"decimal,omitempty"`
	Currency  string   `json:"currency,omitempty"`
	OpenIDs   []string `json:"open_ids,omitempty"`
	FileCodes []string `json:"file_codes,omitempty"`
}

type definitionControl struct {
	ID       string              `json:"id"`
	CustomID string              `json:"custom_id"`
	Type     string              `json:"type"`
	Required bool                `json:"required"`
	Children []definitionControl `json:"children"`
}
type instanceControl struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Value    json.RawMessage `json:"value,omitempty"`
	OpenIDs  []string        `json:"open_ids,omitempty"`
	Currency string          `json:"currency,omitempty"`
}

func templateControls(definition *larkapproval.GetApprovalRespData) ([]definitionControl, error) {
	if definition == nil || definition.Status == nil || *definition.Status != "ACTIVE" || definition.Form == nil {
		return nil, fmt.Errorf("an active approval definition with a form is required")
	}
	var controls []definitionControl
	if err := json.Unmarshal([]byte(*definition.Form), &controls); err != nil || len(controls) == 0 {
		return nil, fmt.Errorf("approval definition form must be a nonempty control array")
	}
	return controls, nil
}

func selectControl(controls []definitionControl, selector ControlSelector) (definitionControl, error) {
	if (selector.ID == "") == (selector.CustomID == "") {
		return definitionControl{}, fmt.Errorf("control selector requires exactly one of id or custom_id")
	}
	var matches []definitionControl
	for _, c := range controls {
		if (selector.ID != "" && c.ID == selector.ID) || (selector.CustomID != "" && c.CustomID == selector.CustomID) {
			matches = append(matches, c)
		}
	}
	if len(matches) != 1 || matches[0].ID == "" {
		return definitionControl{}, fmt.Errorf("control selector must match exactly one system ID in the current template")
	}
	return matches[0], nil
}

// BuildDetailForm emits only native protocol JSON. Original invoice facts and
// common grouping/version logic are supplied by callers and do not live here.
func BuildDetailForm(definition *larkapproval.GetApprovalRespData, binding DetailFormBinding, rows []map[string]FormValue) (json.RawMessage, error) {
	controls, err := templateControls(definition)
	if err != nil {
		return nil, err
	}
	detail, err := selectControl(controls, binding.Detail)
	if err != nil || detail.Type != "fieldList" || len(rows) == 0 {
		return nil, fmt.Errorf("detail binding must select a fieldList and contain rows")
	}
	selected := map[string]definitionControl{}
	used := map[string]bool{}
	for semantic, selector := range binding.Fields {
		control, err := selectControl(detail.Children, selector)
		if err != nil || semantic == "" || used[control.ID] {
			return nil, fmt.Errorf("detail field binding is missing, ambiguous or duplicated")
		}
		used[control.ID], selected[semantic] = true, control
	}
	values := make([][]instanceControl, 0, len(rows))
	for n, row := range rows {
		byID := map[string]instanceControl{}
		for semantic, value := range row {
			control, ok := selected[semantic]
			if !ok {
				return nil, fmt.Errorf("row %d contains an unbound business field", n)
			}
			encoded, err := encodeFormValue(control, value)
			if err != nil {
				return nil, fmt.Errorf("row %d field %s: %w", n, semantic, err)
			}
			byID[control.ID] = encoded
		}
		// Template order is stable even though business values are maps.
		items := []instanceControl{}
		for _, c := range detail.Children {
			if value, exists := byID[c.ID]; exists {
				items = append(items, value)
			}
		}
		values = append(values, items)
	}
	rawRows, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	form, err := json.Marshal([]instanceControl{{ID: detail.ID, Type: detail.Type, Value: rawRows}})
	if err != nil {
		return nil, err
	}
	if err := ValidateTemplateForm(definition, form); err != nil {
		return nil, err
	}
	return form, nil
}

func encodeFormValue(c definitionControl, value FormValue) (instanceControl, error) {
	out := instanceControl{ID: c.ID, Type: c.Type}
	var v any
	switch c.Type {
	case "input", "textarea", "date":
		if value.Decimal != "" || value.Currency != "" || len(value.OpenIDs) != 0 || len(value.FileCodes) != 0 {
			return out, fmt.Errorf("text/date control contains incompatible values")
		}
		v = value.Text
	case "amount", "number":
		if value.Text != "" || len(value.OpenIDs) != 0 || len(value.FileCodes) != 0 || (c.Type == "number" && value.Currency != "") {
			return out, fmt.Errorf("numeric control contains incompatible values")
		}
		amount, ok := invoice.Decimal(value.Decimal)
		if !ok {
			return out, fmt.Errorf("numeric control requires an exact unambiguous decimal")
		}
		v, out.Currency = amount, value.Currency
	case "contact":
		if value.Text != "" || value.Decimal != "" || value.Currency != "" || len(value.FileCodes) != 0 {
			return out, fmt.Errorf("contact control contains incompatible values")
		}
		out.OpenIDs = value.OpenIDs
		return out, nil
	case "attachmentV2":
		if value.Text != "" || value.Decimal != "" || value.Currency != "" || len(value.OpenIDs) != 0 {
			return out, fmt.Errorf("attachment control contains incompatible values")
		}
		v = value.FileCodes
	default:
		return out, fmt.Errorf("control type is not supported by the current mapper")
	}
	raw, err := json.Marshal(v)
	out.Value = raw
	return out, err
}

func decodeForm(raw json.RawMessage) ([]instanceControl, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("approval form must contain exactly one JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var controls []instanceControl
	if err := decoder.Decode(&controls); err != nil || len(controls) == 0 {
		return nil, fmt.Errorf("approval form must be a nonempty control array")
	}
	if err := validateControls(controls, 0); err != nil {
		return nil, err
	}
	return controls, nil
}

func validateInstanceForm(raw json.RawMessage) error {
	_, err := decodeForm(raw)
	return err
}

func validateControls(controls []instanceControl, depth int) error {
	if depth > 1 {
		return fmt.Errorf("nested repeatable detail controls are not supported")
	}
	seen := map[string]bool{}
	for _, c := range controls {
		if c.ID == "" || c.ID != strings.TrimSpace(c.ID) || seen[c.ID] {
			return fmt.Errorf("approval control IDs are required and unique within each row")
		}
		seen[c.ID] = true
		if c.Type != "contact" && len(c.OpenIDs) != 0 || c.Type != "amount" && c.Currency != "" {
			return fmt.Errorf("approval control contains incompatible identity or currency parameters")
		}
		switch c.Type {
		case "input", "textarea", "date":
			var value string
			if err := json.Unmarshal(c.Value, &value); err != nil {
				return fmt.Errorf("approval text/date value must be a string")
			}
			if c.Type == "date" {
				if _, err := time.Parse(time.RFC3339, value); err != nil {
					return fmt.Errorf("approval date must contain an RFC3339 timestamp and offset")
				}
			}
		case "amount", "number":
			decoder := json.NewDecoder(bytes.NewReader(c.Value))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				return fmt.Errorf("approval amount must be a JSON number")
			}
			number, ok := value.(json.Number)
			if _, valid := invoice.Decimal(string(number)); !ok || !valid {
				return fmt.Errorf("approval amount must be an exact decimal JSON number")
			}
			if c.Type == "amount" && (c.Currency == "" || c.Currency != strings.TrimSpace(c.Currency)) {
				return fmt.Errorf("approval amount requires an explicit currency")
			}
		case "contact":
			if len(c.Value) != 0 || len(c.OpenIDs) == 0 {
				return fmt.Errorf("contact controls require open_ids from the approval application")
			}
			for _, id := range c.OpenIDs {
				if !validOpenID(id) {
					return fmt.Errorf("invalid contact open ID")
				}
			}
		case "attachmentV2":
			var codes []string
			if err := json.Unmarshal(c.Value, &codes); err != nil || len(codes) == 0 {
				return fmt.Errorf("attachments require uploaded approval file codes")
			}
			for _, code := range codes {
				if code == "" || code != strings.TrimSpace(code) {
					return fmt.Errorf("invalid approval file code")
				}
			}
		case "fieldList":
			var rows [][]instanceControl
			decoder := json.NewDecoder(bytes.NewReader(c.Value))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&rows); err != nil || len(rows) == 0 {
				return fmt.Errorf("detail control requires a nonempty two-dimensional row array")
			}
			for _, row := range rows {
				if len(row) == 0 {
					return fmt.Errorf("approval detail row is empty")
				}
				if err := validateControls(row, depth+1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("approval control type is not supported by the current mapper")
		}
	}
	return nil
}

// ValidateTemplateForm rejects old/copied widget IDs and missing required
// fields. Complex conditions and unsupported widgets require a separate mapper.
func ValidateTemplateForm(definition *larkapproval.GetApprovalRespData, form json.RawMessage) error {
	controls, err := templateControls(definition)
	if err != nil {
		return err
	}
	values, err := decodeForm(form)
	if err != nil {
		return err
	}
	return matchTemplate(controls, values)
}

func matchTemplate(controls []definitionControl, values []instanceControl) error {
	byID := map[string]definitionControl{}
	for _, c := range controls {
		if c.ID == "" || byID[c.ID].ID != "" {
			return fmt.Errorf("approval definition contains empty or duplicate system IDs")
		}
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	for _, value := range values {
		c, exists := byID[value.ID]
		if !exists || c.Type != value.Type {
			return fmt.Errorf("approval form control does not match the current definition")
		}
		seen[value.ID] = true
		if c.Required && (c.Type == "input" || c.Type == "textarea") {
			var text string
			if err := json.Unmarshal(value.Value, &text); err != nil || strings.TrimSpace(text) == "" {
				return fmt.Errorf("required approval control is empty")
			}
		}
		if c.Type == "fieldList" {
			var rows [][]instanceControl
			if err := json.Unmarshal(value.Value, &rows); err != nil {
				return err
			}
			for _, row := range rows {
				if err := matchTemplate(c.Children, row); err != nil {
					return err
				}
			}
		}
	}
	for _, c := range controls {
		if c.Required && !seen[c.ID] {
			return fmt.Errorf("approval form omits a required control")
		}
	}
	return nil
}
