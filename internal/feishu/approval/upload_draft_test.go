package approval

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

func TestUploadDraftValidatesAllRequiredValuesAndRowsBeforeSendingFiles(t *testing.T) {
	for _, scenario := range []string{"valid", "missing text", "wrong amount type", "missing second file", "foreign target", "wrong purpose", "wrong control", "changed template"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			gateway, source, definition := gatewayFixture(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("instance endpoint forbidden")
			})
			controls, _ := templateControls(definition)
			for n := range controls[0].Children {
				if controls[0].Children[n].Type == "attachmentV2" {
					controls[0].Children[n].Required = true
				}
			}
			raw, _ := json.Marshal(controls)
			form := string(raw)
			definition.Form = &form
			target, err := gateway.Describe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			payloads := []app.UploadPayload{}
			for n := range source.rows {
				fields := map[string]core.Value{}
				for semantic, value := range source.rows[n].Fields {
					if semantic != "invoice_files" {
						fields[semantic] = value
					}
				}
				source.rows[n].Fields = fields
				request, data := nativeUploadRequest(t, gateway.TargetScope())
				request.RecordID = source.rows[n].RecordID
				request, err = core.NewUploadRequest(request)
				if err != nil {
					t.Fatal(err)
				}
				payloads = append(payloads, app.UploadPayload{Request: request, Data: data, Semantic: "invoice_files"})
			}
			switch scenario {
			case "missing text":
				for _, control := range controls[0].Children {
					if control.Required && control.Type == "input" {
						delete(source.rows[1].Fields, control.CustomID)
						break
					}
				}
			case "wrong amount type":
				for _, control := range controls[0].Children {
					if control.Type == "amount" {
						source.rows[0].Fields[control.CustomID] = core.Value{Kind: "text", Text: "10"}
						break
					}
				}
			case "missing second file":
				payloads = payloads[:1]
			case "foreign target":
				payloads[0].Request.TargetScope = "other"
				payloads[0].Request, _ = core.NewUploadRequest(payloads[0].Request)
			case "wrong purpose":
				payloads[0].Request.Kind = "image"
				payloads[0].Request, _ = core.NewUploadRequest(payloads[0].Request)
			case "wrong control":
				payloads[0].Semantic = "country"
			case "changed template":
				form += " "
				definition.Form = &form
			}
			err = gateway.ValidateUploadDraft(context.Background(), target.ConfigurationVersion, source.rows, payloads)
			if (err == nil) != (scenario == "valid") || calls != 0 {
				t.Fatal("draft validation bypassed required fields, scope, template or instance boundary")
			}
			if scenario == "valid" && *definition.Form != string(raw) {
				t.Fatal("local deferred attachment validation changed the actual template")
			}
			if scenario == "changed template" && !errors.Is(err, core.ErrPlanChanged) {
				t.Fatal("template edit did not stop upload preparation")
			}
			if scenario == "valid" {
				rows, _ := gateway.formRows(definition, source.rows)
				if _, err := BuildDetailForm(definition, gateway.binding, rows); err == nil {
					t.Fatal("pending draft unexpectedly became an executable form")
				}
			}
		})
	}
}
