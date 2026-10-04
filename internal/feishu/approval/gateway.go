package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

// InstanceGateway is the only layer that turns neutral plans into native form
// JSON. Target identity is derived from explicit client credentials, never Base.
type InstanceGateway struct {
	*InstanceLookupGateway
	template      string
	binding       DetailFormBinding
	nodeApprovers map[string][]string
}

// InstanceLookupGateway needs only the saved plan and its original application.
// It deliberately has no creation methods or current template/form configuration.
type InstanceLookupGateway struct{ client *InstanceClient }

func NewInstanceLookupGateway(client *InstanceClient) (*InstanceLookupGateway, error) {
	if client == nil || client.TargetScope() == "" {
		return nil, fmt.Errorf("approval lookup requires an explicit application identity")
	}
	return &InstanceLookupGateway{client: client}, nil
}

func (g *InstanceLookupGateway) TargetScope() string { return g.client.TargetScope() }

func NewInstanceGateway(client *InstanceClient, template string, binding DetailFormBinding, nodeApprovers map[string][]string) (*InstanceGateway, error) {
	lookup, err := NewInstanceLookupGateway(client)
	if err != nil || template == "" || len(binding.Fields) == 0 {
		return nil, fmt.Errorf("approval gateway requires explicit identity, template and field binding")
	}
	// Freeze constructor input; caller map edits cannot change a prepared plan.
	body, err := json.Marshal(struct {
		Binding DetailFormBinding
		Nodes   map[string][]string
	}{binding, nodeApprovers})
	if err != nil {
		return nil, err
	}
	var frozen struct {
		Binding DetailFormBinding
		Nodes   map[string][]string
	}
	if err := json.Unmarshal(body, &frozen); err != nil {
		return nil, err
	}
	for _, people := range frozen.Nodes {
		sort.Strings(people)
	}
	return &InstanceGateway{lookup, template, frozen.Binding, frozen.Nodes}, nil
}

func (g *InstanceGateway) describe(ctx context.Context) (*larkapproval.GetApprovalRespData, app.Target, error) {
	definition, err := g.client.GetDefinition(ctx, g.template)
	if err != nil {
		return nil, app.Target{}, &instanceTransportError{operation: "definition", cause: err}
	}
	if _, err := templateControls(definition); err != nil {
		return nil, app.Target{}, err
	}
	if err := g.validateApprovers(definition); err != nil {
		return nil, app.Target{}, err
	}
	// Form options and process nodes are pinned along with local bindings. An
	// edited template must not silently reinterpret an old prepared plan.
	body, err := json.Marshal(struct {
		Definition *larkapproval.GetApprovalRespData
		Binding    DetailFormBinding
		Nodes      map[string][]string
	}{definition, g.binding, g.nodeApprovers})
	if err != nil {
		return nil, app.Target{}, err
	}
	hash := sha256.Sum256(body)
	return definition, app.Target{Scope: g.TargetScope(), Template: g.template, ConfigurationVersion: hex.EncodeToString(hash[:])}, nil
}

func (g *InstanceGateway) Describe(ctx context.Context) (app.Target, error) {
	_, target, err := g.describe(ctx)
	return target, err
}

func (g *InstanceGateway) validateApprovers(definition *larkapproval.GetApprovalRespData) error {
	if len(definition.NodeList) == 0 || len(g.nodeApprovers) > 20 {
		return fmt.Errorf("approval process nodes are missing or exceed the native API limit")
	}
	nodes := map[string]*larkapproval.ApprovalNodeInfo{}
	for _, node := range definition.NodeList {
		if node == nil || node.NodeId == nil || *node.NodeId == "" || node.NeedApprover == nil || nodes[*node.NodeId] != nil {
			return fmt.Errorf("approval definition has incomplete or duplicate process nodes")
		}
		nodes[*node.NodeId] = node
		if *node.NeedApprover && len(g.nodeApprovers[*node.NodeId]) == 0 {
			return fmt.Errorf("approval process requires explicitly selected approvers")
		}
	}
	for id, people := range g.nodeApprovers {
		node := nodes[id]
		if node == nil || !*node.NeedApprover || len(people) == 0 ||
			(len(people) > 1 && (node.ApproverChosenMulti == nil || !*node.ApproverChosenMulti)) {
			return fmt.Errorf("approval node selection does not match the current process")
		}
		seen := map[string]bool{}
		for _, person := range people {
			if !validOpenID(person) || seen[person] {
				return fmt.Errorf("approval node requires unique application open IDs")
			}
			seen[person] = true
		}
	}
	return nil
}

func (g *InstanceGateway) request(ctx context.Context, plan core.Plan) (InstanceRequest, error) {
	if err := plan.Validate(); err != nil {
		return InstanceRequest{}, err
	}
	if plan.TargetScope != g.TargetScope() || plan.Template != g.template {
		return InstanceRequest{}, fmt.Errorf("approval plan belongs to another gateway identity/template")
	}
	definition, target, err := g.describe(ctx)
	if err != nil {
		return InstanceRequest{}, err
	}
	if target.ConfigurationVersion != plan.ConfigurationVersion {
		return InstanceRequest{}, core.ErrPlanChanged
	}
	controls, _ := templateControls(definition)
	detail, err := selectControl(controls, g.binding.Detail)
	if err != nil || detail.Type != "fieldList" {
		return InstanceRequest{}, fmt.Errorf("approval binding must select a current detail control")
	}
	kinds := map[string]string{}
	for semantic, selector := range g.binding.Fields {
		control, err := selectControl(detail.Children, selector)
		if err != nil {
			return InstanceRequest{}, err
		}
		kinds[semantic] = map[string]string{"input": "text", "textarea": "text", "date": "date", "amount": "money", "number": "number", "contact": "people", "attachmentV2": "files"}[control.Type]
	}
	rows := make([]map[string]FormValue, 0, len(plan.Rows))
	for _, row := range plan.Rows {
		fields := make(map[string]FormValue, len(row.Fields))
		for semantic, value := range row.Fields {
			if kinds[semantic] != value.Kind {
				return InstanceRequest{}, fmt.Errorf("approval business field %s has a different type from its current template control", semantic)
			}
			v := FormValue{Text: value.Text, Decimal: value.Decimal, Currency: value.Currency}
			for _, ref := range value.References {
				if value.Kind == "people" {
					v.OpenIDs = append(v.OpenIDs, ref.ID)
				} else if value.Kind == "files" {
					v.FileCodes = append(v.FileCodes, ref.ID)
				}
			}
			fields[semantic] = v
		}
		rows = append(rows, fields)
	}
	form, err := BuildDetailForm(definition, g.binding, rows)
	if err != nil {
		return InstanceRequest{}, err
	}
	request := InstanceRequest{ApprovalCode: g.template, UUID: plan.ID, OpenID: plan.Submitter.ID, DepartmentID: plan.DepartmentID, Form: form, NodeApprovers: g.nodeApprovers}
	return request, request.Validate()
}

func (g *InstanceGateway) ValidatePlan(ctx context.Context, plan core.Plan) error {
	_, err := g.request(ctx, plan)
	return err
}

func (g *InstanceGateway) Create(ctx context.Context, plan core.Plan) (core.Instance, error) {
	request, err := g.request(ctx, plan)
	if err != nil {
		// No creation request was sent, including a template change after Begin.
		return core.Instance{}, errors.Join(core.ErrRejected, err)
	}
	ref, err := g.client.CreateInstance(ctx, request)
	if err != nil {
		var remote *InstanceAPIError
		if errors.As(err, &remote) && remote.HTTPStatus() == 400 {
			switch remote.Code {
			case 1390001, 1390013, 1390015, 99991672:
				return core.Instance{}, errors.Join(core.ErrRejected, err)
			}
		}
		// UUID collision, provider internal error and transport failures stay
		// uncertain. Only a documented definitive rejection releases occupancy.
		return core.Instance{}, err
	}
	return core.Instance{ID: ref.Code, UUID: plan.ID, TargetScope: g.TargetScope(), Template: plan.Template, SubmitterID: plan.Submitter.ID, Status: "pending"}, nil
}

func (g *InstanceLookupGateway) Lookup(ctx context.Context, plan core.Plan) (core.Instance, error) {
	if err := plan.Validate(); err != nil {
		return core.Instance{}, err
	}
	if plan.TargetScope != g.TargetScope() {
		return core.Instance{}, fmt.Errorf("approval lookup belongs to another target identity")
	}
	// Lookup uses the saved template/UUID, not a newly selected template or form.
	data, err := g.client.GetInstance(ctx, plan.ID)
	if err != nil {
		return core.Instance{}, err
	}
	if data.Uuid == nil || data.ApprovalCode == nil || data.OpenId == nil {
		return core.Instance{}, core.ErrConflict
	}
	status := map[string]string{"PENDING": "pending", "APPROVED": "approved", "REJECTED": "rejected", "CANCELED": "canceled", "DELETED": "deleted"}[*data.Status]
	instance := core.Instance{ID: *data.InstanceCode, UUID: *data.Uuid, TargetScope: g.TargetScope(), Template: *data.ApprovalCode, SubmitterID: *data.OpenId, Status: status, Verified: true}
	return instance, instance.Validate(plan)
}
