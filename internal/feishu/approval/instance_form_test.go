package approval

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
)

func formFixture(t *testing.T) (*larkapproval.GetApprovalRespData, DetailFormBinding, map[string]FormValue) {
	t.Helper()
	body, err := os.ReadFile("../../../external-api/feishu/approval-definition-EA296788-7BFC-47A2-91D7-6B7D8D2D0B11.json")
	if err != nil {
		t.Fatal(err)
	}
	var response larkapproval.GetApprovalResp
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	binding := DetailFormBinding{Detail: ControlSelector{CustomID: "expense_details"}, Fields: map[string]ControlSelector{}}
	controls, err := templateControls(response.Data)
	if err != nil {
		t.Fatal(err)
	}
	// Use custom IDs from the actual API-created template, not its old request IDs.
	binding.Detail.CustomID = controls[0].CustomID
	values := map[string]FormValue{}
	for _, c := range controls[0].Children {
		binding.Fields[c.CustomID] = ControlSelector{CustomID: c.CustomID}
		switch c.Type {
		case "input", "textarea":
			values[c.CustomID] = FormValue{Text: "test"}
		case "date":
			values[c.CustomID] = FormValue{Text: "2026-10-05T12:00:00+08:00"}
		case "amount":
			values[c.CustomID] = FormValue{Decimal: "9007199254740993.12", Currency: "CNY"}
		case "contact":
			values[c.CustomID] = FormValue{OpenIDs: []string{"ou_test"}}
		case "attachmentV2":
			values[c.CustomID] = FormValue{FileCodes: []string{"approval-file-code"}}
		default:
			t.Fatalf("unexpected fixture control type %s", c.Type)
		}
	}
	return response.Data, binding, values
}

func TestDetailMapperUsesActualWidgetIDsAndPreservesEveryRow(t *testing.T) {
	definition, binding, row := formFixture(t)
	form, err := BuildDetailForm(definition, binding, []map[string]FormValue{row, row})
	if err != nil {
		t.Fatal(err)
	}
	var controls []instanceControl
	if err := json.Unmarshal(form, &controls); err != nil {
		t.Fatal(err)
	}
	var rows [][]instanceControl
	if err := json.Unmarshal(controls[0].Value, &rows); err != nil || len(rows) != 2 || len(rows[0]) != 14 {
		t.Fatal("repeatable detail dropped a row or required field")
	}
	if !strings.HasPrefix(controls[0].ID, "widget") || controls[0].ID == binding.Detail.CustomID || strings.Count(string(form), "9007199254740993.12") != 4 {
		t.Fatal("mapper used semantic IDs on wire or rounded money")
	}
	if err := ValidateTemplateForm(definition, form); err != nil {
		t.Fatal(err)
	}
}

func TestDetailMapperRejectsIncompleteAndForeignMappings(t *testing.T) {
	for _, scenario := range []string{"missing required", "blank required", "wrong template ID", "ambiguous custom ID", "duplicate binding", "missing currency", "invalid date", "contact name", "unsupported mutableGroup", "unused supplied value"} {
		t.Run(scenario, func(t *testing.T) {
			definition, binding, row := formFixture(t)
			controls, _ := templateControls(definition)
			first := controls[0].Children[0]
			switch scenario {
			case "missing required":
				delete(row, first.CustomID)
			case "blank required":
				for _, c := range controls[0].Children {
					if c.Required && c.Type == "input" {
						row[c.CustomID] = FormValue{Text: "  "}
						break
					}
				}
			case "wrong template ID":
				binding.Detail = ControlSelector{ID: "another-tenant-widget"}
			case "ambiguous custom ID":
				controls[0].Children[1].CustomID = first.CustomID
				raw, _ := json.Marshal(controls)
				text := string(raw)
				definition.Form = &text
			case "duplicate binding":
				binding.Fields["duplicate"] = binding.Fields[first.CustomID]
			case "missing currency", "invalid date", "contact name":
				for _, c := range controls[0].Children {
					if scenario == "missing currency" && c.Type == "amount" {
						row[c.CustomID] = FormValue{Decimal: "10"}
					} else if scenario == "invalid date" && c.Type == "date" {
						row[c.CustomID] = FormValue{Text: "2026-10-05"}
					} else if scenario == "contact name" && c.Type == "contact" {
						row[c.CustomID] = FormValue{OpenIDs: []string{"Alice"}}
					}
				}
			case "unsupported mutableGroup":
				controls[0].Type = "mutableGroup"
				raw, _ := json.Marshal(controls)
				text := string(raw)
				definition.Form = &text
			case "unused supplied value":
				row["unknown"] = FormValue{Text: "not mapped"}
			}
			if _, err := BuildDetailForm(definition, binding, []map[string]FormValue{row}); err == nil {
				t.Fatal("unsafe or incomplete native form accepted")
			}
		})
	}
}
