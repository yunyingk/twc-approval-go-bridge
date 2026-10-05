package base

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
)

type approvalCell struct {
	kind   int
	target string
}
type approvalSourceTransport struct {
	schema   map[string]map[string]approvalCell
	values   map[string]map[string]json.RawMessage
	relation json.RawMessage
	deny     string
	reads    map[string]int
}

func (s *approvalSourceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var payload any
	if strings.HasSuffix(req.URL.Path, "/tenant_access_token/internal") && req.Method == http.MethodPost {
		var auth map[string]string
		json.NewDecoder(req.Body).Decode(&auth)
		if auth["app_id"] != "bridge" {
			return nil, fmt.Errorf("wrong source identity")
		}
		payload = map[string]any{"code": 0, "tenant_access_token": "source-token"}
	} else {
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer source-token" {
			return nil, fmt.Errorf("non-read source operation")
		}
		parts := strings.Split(req.URL.Path, "/")
		table := parts[7]
		if strings.HasSuffix(req.URL.Path, "/fields") {
			items := []any{}
			for id, cell := range s.schema[table] {
				items = append(items, map[string]any{"field_id": id, "field_name": "Renamed " + id, "type": cell.kind, "property": map[string]any{"table_id": cell.target, "multiple": true}})
			}
			payload = map[string]any{"code": 0, "data": map[string]any{"items": items}}
		} else if strings.Contains(req.URL.Path, "/records/") {
			if req.URL.Query().Get("user_id_type") != "open_id" || req.URL.Query().Get("text_field_as_array") != "true" {
				return nil, fmt.Errorf("identity/text response options missing")
			}
			id := parts[len(parts)-1]
			s.reads[table+":"+id]++
			if s.deny == table {
				return nil, fmt.Errorf("PRIVATE_SECRET_REMOTE_ERROR")
			}
			fields := map[string]json.RawMessage{}
			for field, value := range s.values[table] {
				fields["Renamed "+field] = value
			}
			if table == "details" {
				fields["Renamed transaction"] = s.relation
			}
			payload = map[string]any{"code": 0, "data": map[string]any{"record": map[string]any{"record_id": id, "fields": fields}}}
		} else {
			return nil, fmt.Errorf("unexpected API path")
		}
	}
	raw, _ := json.Marshal(payload)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
}

func approvalSourceFixture(t *testing.T) (*LedgerClient, ApprovalSourceBinding, *approvalSourceTransport) {
	t.Helper()
	client := NewLedgerClient("bridge", "secret")
	binding := ApprovalSourceBinding{BaseToken: "base", DetailTableID: "details", TargetScope: "feishu-app:bridge", Inputs: map[string]ApprovalInputBinding{
		"amount":   {Role: "reimbursement_details", FieldID: "amount", Kind: "money", CurrencyFieldID: "currency"},
		"employee": {Role: "reimbursement_details", FieldID: "employee", Kind: "people"},
		"reason":   {Role: "reimbursement_details", FieldID: "reason", Kind: "text"},
		"date":     {Role: "reimbursement_details", FieldID: "date", Kind: "date"},
	}, Groups: []ApprovalGroupBinding{{Axis: "project", Role: "reimbursement_details", FieldID: "project"}, {Axis: "day", Role: "reimbursement_details", FieldID: "date", Period: "day", Timezone: "Asia/Shanghai"}}}
	transport := &approvalSourceTransport{reads: map[string]int{}, relation: json.RawMessage(`[{"table_id":"transactions","record_ids":["recPayment"],"text":"NOT-AN-ID"}]`), schema: map[string]map[string]approvalCell{
		"details":      {"amount": {2, ""}, "currency": {3, ""}, "employee": {11, ""}, "reason": {1, ""}, "date": {5, ""}, "project": {21, "projects"}, "files": {17, ""}, "transaction": {21, "transactions"}},
		"transactions": {"amount": {2, ""}, "currency": {3, ""}, "merchant": {1, ""}},
	}, values: map[string]map[string]json.RawMessage{
		"details":      {"amount": json.RawMessage(`9007199254740993.01`), "currency": json.RawMessage(`"usd"`), "employee": json.RawMessage(`[{"id":"ou_employee","name":"PRIVATE_PERSON","email":"PRIVATE_EMAIL"}]`), "reason": json.RawMessage(`[{"type":"text","text":"PRIVATE_REASON"}]`), "date": json.RawMessage(fmt.Sprint(time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC).UnixMilli())), "project": json.RawMessage(`[{"table_id":"projects","record_ids":["recProject"],"text":"SAME DISPLAY NAME"}]`), "files": json.RawMessage(`[{"file_token":"BASE_TOKEN_MUST_NOT_LEAK"}]`)},
		"transactions": {"amount": json.RawMessage(`9007199254740993.02`), "currency": json.RawMessage(`"USD"`), "merchant": json.RawMessage(`"PRIVATE_MERCHANT"`)},
	}}
	client.httpClient.Transport = transport
	return client, binding, transport
}

func readApprovalInputs(t *testing.T, client *LedgerClient, binding ApprovalSourceBinding) []app.InputCheck {
	t.Helper()
	source, err := NewApprovalFieldsSource(client, binding)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := source.ReadApprovalInputs(context.Background(), []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	return checks
}

func hasInputIssue(check app.InputCheck, code string) bool {
	for _, issue := range check.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestApprovalSourceKeepsExactMoneyIdentityAndConfiguredTimeBucket(t *testing.T) {
	client, binding, transport := approvalSourceFixture(t)
	checks := readApprovalInputs(t, client, binding)
	if len(checks) != 2 || checks[0].RecordID != "a" || len(checks[0].Issues) > 0 {
		t.Fatal("source was not prepared in stable order")
	}
	row := checks[0].Row
	if row.Fields["amount"].Decimal != "9007199254740993.01" || row.Fields["amount"].Currency != "USD" || row.Fields["employee"].References[0].ID != "ou_employee" || row.GroupValues["day"] != "2026-10-01" || row.GroupValues["project"] != "feishu:base:projects:recProject" || len(row.SourceVersion) != 64 {
		t.Fatal("typed source lost precision, identity or timezone")
	}
	raw, _ := json.Marshal(checks)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("form values leaked through diagnostics")
	}
	transport.values["details"]["project"] = json.RawMessage(`[{"table_id":"projects","record_ids":["recProject"],"text":"RENAMED PROJECT"}]`)
	again := readApprovalInputs(t, client, binding)
	if !reflect.DeepEqual(checks[0].Row, again[0].Row) {
		t.Fatal("a relation display rename changed its business identity")
	}
	transport.values["details"]["project"] = json.RawMessage(`[{"table_id":"projects","record_ids":["recDifferent"],"text":"SAME DISPLAY NAME"}]`)
	if readApprovalInputs(t, client, binding)[0].Row.GroupValues["project"] == row.GroupValues["project"] {
		t.Fatal("same-name projects merged")
	}
}

func TestApprovalSourceDoesNotInferPeopleOrUseBaseTokensAsApprovalFiles(t *testing.T) {
	for _, scenario := range []string{"missing map", "partial map", "mapped", "mapping collapse", "wrong identifier", "files"} {
		t.Run(scenario, func(t *testing.T) {
			client, binding, transport := approvalSourceFixture(t)
			binding.TargetScope = "feishu-app:approval"
			input := binding.Inputs["employee"]
			input.PersonMap = map[string]string{"ou_employee": "ou_target"}
			want := ""
			switch scenario {
			case "missing map":
				input.PersonMap = nil
				want = "person_mapping_missing"
			case "partial map":
				input.PersonMap = map[string]string{"ou_other": "ou_target"}
				want = "person_mapping_missing"
			case "mapping collapse":
				input.PersonMap["ou_second"] = "ou_target"
				transport.values["details"]["employee"] = json.RawMessage(`[{"id":"ou_employee"},{"id":"ou_second"}]`)
				want = "person_mapping_ambiguous"
			case "wrong identifier":
				transport.values["details"]["employee"] = json.RawMessage(`[{"id":"user_identifier","name":"same name"}]`)
				want = "person_id_invalid"
			case "files":
				binding.Inputs["files"] = ApprovalInputBinding{Role: "reimbursement_details", FieldID: "files", Kind: "files"}
				want = "approval_upload_required"
			}
			binding.Inputs["employee"] = input
			checks := readApprovalInputs(t, client, binding)
			if want == "" {
				if len(checks[0].Issues) != 0 || checks[0].Row.Fields["employee"].References[0].ID != "ou_target" {
					t.Fatal("explicit mapping was not used")
				}
			} else if !hasInputIssue(checks[0], want) {
				t.Fatalf("missing blocker %s", want)
			}
			raw, _ := json.Marshal(checks)
			if strings.Contains(string(raw), "BASE_TOKEN") {
				t.Fatal("source file token exposed")
			}
		})
	}
}

func TestApprovalTransactionProjectionRequiresExactlyOneLinkedPayment(t *testing.T) {
	for _, scenario := range []string{"one", "none", "multiple", "wrong target", "permission"} {
		t.Run(scenario, func(t *testing.T) {
			client, binding, transport := approvalSourceFixture(t)
			binding.TransactionTableID, binding.TransactionRelationFieldID = "transactions", "transaction"
			binding.Inputs["payment_amount"] = ApprovalInputBinding{Role: "transactions", FieldID: "amount", Kind: "money", CurrencyFieldID: "currency"}
			want := ""
			switch scenario {
			case "none":
				transport.relation = json.RawMessage(`[]`)
				want = "transaction_missing"
			case "multiple":
				transport.relation = json.RawMessage(`[{"record_ids":["recA","recB"],"table_id":"transactions"}]`)
				want = "transaction_ambiguous"
			case "wrong target":
				transport.schema["details"]["transaction"] = approvalCell{21, "old-table"}
				want = "transaction_relation_invalid"
			case "permission":
				transport.deny = "transactions"
				want = "transaction_unavailable"
			}
			checks := readApprovalInputs(t, client, binding)
			if want == "" {
				if len(checks[0].Issues) > 0 || checks[0].Row.Fields["payment_amount"].Decimal != "9007199254740993.02" || transport.reads["transactions:recPayment"] != 1 {
					t.Fatal("linked payment was guessed, rounded or read repeatedly")
				}
			} else if !hasInputIssue(checks[0], want) {
				t.Fatalf("missing blocker %s", want)
			}
			if scenario != "one" && scenario != "permission" && transport.reads["transactions:recPayment"] != 0 {
				t.Fatal("ambiguous relation fetched a payment")
			}
			raw, _ := json.Marshal(checks)
			if strings.Contains(string(raw), "PRIVATE_SECRET_REMOTE_ERROR") {
				t.Fatal("remote error exposed")
			}
		})
	}
}

func TestApprovalSourceBindingsAreFrozenAndVersioned(t *testing.T) {
	client, binding, _ := approvalSourceFixture(t)
	source, err := NewApprovalFieldsSource(client, binding)
	if err != nil {
		t.Fatal(err)
	}
	reordered := binding
	reordered.Groups = []ApprovalGroupBinding{binding.Groups[1], binding.Groups[0]}
	again, err := NewApprovalFieldsSource(client, reordered)
	if err != nil || again.version != source.version {
		t.Fatal("group order changed binding version")
	}
	amount := binding.Inputs["amount"]
	amount.CurrencyFieldID = ""
	amount.Currency = "USD"
	binding.Inputs["amount"] = amount
	changed, err := NewApprovalFieldsSource(client, binding)
	if err != nil || changed.scope != source.scope || changed.version == source.version {
		t.Fatal("mapping change lost version or changed stable source scope")
	}
	if source.binding.Inputs["amount"].CurrencyFieldID != "currency" {
		t.Fatal("caller mutated frozen binding")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.ReadApprovalInputs(ctx, []string{"a"}); err != context.Canceled {
		t.Fatal("canceled source read continued")
	}
	for _, ids := range [][]string{{}, {"a", "a"}, {" a"}} {
		if _, err := source.ReadApprovalInputs(context.Background(), ids); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
}

func TestApprovalSourceRejectsIncompatibleOrAmbiguousNativeCells(t *testing.T) {
	for _, scenario := range []string{"formula amount", "nondecimal", "currency", "date", "multiple projects", "display only", "mention"} {
		t.Run(scenario, func(t *testing.T) {
			client, binding, transport := approvalSourceFixture(t)
			want := "value_invalid"
			switch scenario {
			case "formula amount":
				transport.schema["details"]["amount"] = approvalCell{20, ""}
				want = "field_type_invalid"
			case "nondecimal":
				transport.values["details"]["amount"] = json.RawMessage(`"1e20"`)
			case "currency":
				transport.values["details"]["currency"] = json.RawMessage(`"US$"`)
				want = "currency_invalid"
			case "date":
				transport.values["details"]["date"] = json.RawMessage(`1790800000.5`)
			case "multiple projects":
				transport.values["details"]["project"] = json.RawMessage(`[{"record_ids":["recA","recB"],"table_id":"projects"}]`)
				want = "group_value_ambiguous"
			case "display only":
				transport.values["details"]["project"] = json.RawMessage(`[{"table_id":"projects","text":"project name"}]`)
				want = "relation_value_invalid"
			case "mention":
				transport.values["details"]["reason"] = json.RawMessage(`[{"type":"mention","text":"employee name"}]`)
			}
			if !hasInputIssue(readApprovalInputs(t, client, binding)[0], want) {
				t.Fatalf("unsafe cell accepted: %s", scenario)
			}
		})
	}
}
