package base

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/core/invoice"
)

func approvalCurrency(value string) bool {
	return len(value) == 3 && strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == ""
}
func approvalPersonID(value string) bool {
	return strings.HasPrefix(value, "ou_") && len(value) > 3 && value == strings.TrimSpace(value)
}
func absentCell(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null"
}

// Rich text is accepted only when every segment is literal text. Mentions,
// formulas and relation display objects must not silently become scalar inputs.
func approvalText(raw json.RawMessage) (string, bool) {
	var scalar string
	if json.Unmarshal(raw, &scalar) == nil {
		return scalar, true
	}
	var segments []struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	if json.Unmarshal(raw, &segments) != nil || segments == nil {
		return "", false
	}
	var result strings.Builder
	for _, segment := range segments {
		if segment.Type != "text" || segment.Text == nil {
			return "", false
		}
		result.WriteString(*segment.Text)
	}
	return result.String(), true
}

func approvalDecimal(raw json.RawMessage, fieldType int) (string, bool) {
	var value string
	if fieldType == 1 {
		text, ok := approvalText(raw)
		if !ok {
			return "", false
		}
		value = text
	} else {
		// Decode into json.Number, never float64, including values above 2^53.
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return "", false
		}
		value = number.String()
	}
	number, ok := invoice.Decimal(value)
	return number.String(), ok
}

func approvalTimestamp(raw json.RawMessage) (time.Time, bool) {
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return time.Time{}, false
	}
	millis, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	value := time.UnixMilli(millis).UTC()
	return value, value.Year() >= 1 && value.Year() <= 9999
}

func approvalPeople(raw json.RawMessage) ([]string, bool) {
	var cells []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &cells) != nil || len(cells) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(cells))
	for _, cell := range cells {
		if !approvalPersonID(cell.ID) || seen[cell.ID] {
			return nil, false
		}
		seen[cell.ID] = true
		ids = append(ids, cell.ID)
	}
	sort.Strings(ids)
	return ids, true
}

func (s *ApprovalFieldsSource) inputValue(input ApprovalInputBinding, schema map[string]ledgerField, fields map[string]json.RawMessage) (core.Value, string) {
	field, exists := schema[input.FieldID]
	if !exists || field.Name == "" {
		return core.Value{}, "field_missing"
	}
	raw := fields[field.Name]
	value := core.Value{Kind: input.Kind}
	wrongType := func(allowed ...int) bool {
		for _, kind := range allowed {
			if field.Type == kind {
				return false
			}
		}
		return true
	}
	switch input.Kind {
	case "text":
		if wrongType(1, 3, 1005) {
			return core.Value{}, "field_type_invalid"
		}
		// Optional text can be empty; requiredness belongs to the live template.
		if absentCell(raw) {
			return value, ""
		}
		text, ok := approvalText(raw)
		if !ok {
			return core.Value{}, "value_invalid"
		}
		value.Text = text
	case "number", "money":
		if wrongType(1, 2) {
			return core.Value{}, "field_type_invalid"
		}
		if absentCell(raw) {
			return core.Value{}, "value_missing"
		}
		decimal, ok := approvalDecimal(raw, field.Type)
		if !ok {
			return core.Value{}, "value_invalid"
		}
		value.Decimal = decimal
		if input.Kind == "money" {
			value.Currency = input.Currency
			if input.CurrencyFieldID != "" {
				currencyField, exists := schema[input.CurrencyFieldID]
				if !exists || currencyField.Name == "" {
					return core.Value{}, "currency_field_missing"
				}
				if currencyField.Type != 1 && currencyField.Type != 3 {
					return core.Value{}, "currency_field_type_invalid"
				}
				currency, ok := approvalText(fields[currencyField.Name])
				if !ok {
					return core.Value{}, "currency_invalid"
				}
				value.Currency = strings.ToUpper(currency)
			}
			if !approvalCurrency(value.Currency) {
				return core.Value{}, "currency_invalid"
			}
		}
	case "date":
		if wrongType(5) {
			return core.Value{}, "field_type_invalid"
		}
		if absentCell(raw) {
			return core.Value{}, "value_missing"
		}
		date, ok := approvalTimestamp(raw)
		if !ok {
			return core.Value{}, "value_invalid"
		}
		value.Text = date.Format(time.RFC3339Nano)
	case "people":
		if wrongType(11) {
			return core.Value{}, "field_type_invalid"
		}
		if absentCell(raw) {
			return core.Value{}, "value_missing"
		}
		ids, ok := approvalPeople(raw)
		if !ok {
			return core.Value{}, "person_id_invalid"
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if s.binding.TargetScope != "feishu-app:"+s.client.appID || len(input.PersonMap) > 0 {
				mapped, ok := input.PersonMap[id]
				if !ok {
					return core.Value{}, "person_mapping_missing"
				}
				id = mapped
			}
			if seen[id] {
				return core.Value{}, "person_mapping_ambiguous"
			}
			seen[id] = true
			value.References = append(value.References, core.Identity{Scope: s.binding.TargetScope, ID: id})
		}
	case "files":
		if wrongType(17) {
			return core.Value{}, "field_type_invalid"
		}
		var files []struct {
			Token string `json:"file_token"`
		}
		if absentCell(raw) || string(bytes.TrimSpace(raw)) == "[]" {
			return core.Value{}, "value_missing"
		}
		if json.Unmarshal(raw, &files) != nil {
			return core.Value{}, "file_reference_invalid"
		}
		if len(files) == 0 {
			return core.Value{}, "value_missing"
		}
		seen := map[string]bool{}
		for _, file := range files {
			if file.Token == "" || file.Token != strings.TrimSpace(file.Token) || seen[file.Token] {
				return core.Value{}, "file_reference_invalid"
			}
			seen[file.Token] = true
		}
		// Base file tokens never become approval attachmentV2 file codes.
		return core.Value{}, "approval_upload_required"
	default:
		return core.Value{}, "field_type_invalid"
	}
	return value, ""
}

func (s *ApprovalFieldsSource) groupValue(group ApprovalGroupBinding, schema map[string]ledgerField, fields map[string]json.RawMessage) (string, string) {
	field, exists := schema[group.FieldID]
	if !exists || field.Name == "" {
		return "", "field_missing"
	}
	raw := fields[field.Name]
	if absentCell(raw) {
		return "", "group_value_missing"
	}
	if group.Period != "" && field.Type != 5 {
		return "", "field_type_invalid"
	}
	switch field.Type {
	case 18, 21:
		if field.RelatedTableID == "" {
			return "", "relation_target_invalid"
		}
		ids, err := relatedRecordIDs(raw, field.RelatedTableID)
		if err != nil {
			return "", "relation_value_invalid"
		}
		if len(ids) == 0 {
			return "", "group_value_missing"
		}
		if len(ids) != 1 {
			return "", "group_value_ambiguous"
		}
		return "feishu:" + s.binding.BaseToken + ":" + field.RelatedTableID + ":" + ids[0], ""
	case 11:
		ids, ok := approvalPeople(raw)
		if !ok {
			return "", "person_id_invalid"
		}
		if len(ids) != 1 {
			return "", "group_value_ambiguous"
		}
		return "feishu-app:" + s.client.appID + ":" + ids[0], ""
	case 5:
		date, ok := approvalTimestamp(raw)
		if !ok {
			return "", "value_invalid"
		}
		if group.Period != "" {
			zone, err := time.LoadLocation(group.Timezone)
			if err != nil {
				return "", "timezone_invalid"
			}
			date = date.In(zone)
			format := "2006-01-02"
			if group.Period == "month" {
				format = "2006-01"
			}
			return date.Format(format), ""
		}
		return date.Format(time.RFC3339Nano), ""
	case 1, 3, 1005:
		value, ok := approvalText(raw)
		if !ok || value == "" || value != strings.TrimSpace(value) {
			return "", "group_value_invalid"
		}
		return "text:" + value, ""
	case 2:
		value, ok := approvalDecimal(raw, field.Type)
		if !ok {
			return "", "value_invalid"
		}
		return "number:" + value, ""
	case 7:
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return "", "value_invalid"
		}
		return "checkbox:" + strconv.FormatBool(value), ""
	default:
		return "", "field_type_invalid"
	}
}
