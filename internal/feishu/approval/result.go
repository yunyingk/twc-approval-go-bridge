package approval

import (
	"encoding/json"
	"strconv"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

func resultText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func nativeResultTime(value *string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(*value, 10, 64)
	if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != *value {
		return nil, core.ErrConflict
	}
	return &parsed, nil
}
func nativeResultActor(scope string, id *string) *core.Identity {
	if id == nil || *id == "" {
		return nil
	}
	return &core.Identity{Scope: scope, ID: *id}
}
func nativeResultFiles(files []*larkapproval.File) ([]core.ResultFile, error) {
	if files == nil {
		return nil, nil
	}
	result := []core.ResultFile{}
	for _, file := range files {
		if file == nil {
			return nil, core.ErrConflict
		}
		var size *int64
		if file.FileSize != nil {
			value := int64(*file.FileSize)
			size = &value
		}
		result = append(result, core.ResultFile{Name: resultText(file.Title), Kind: resultText(file.Type), Size: size})
	}
	return result, nil
}

func nativeResultSnapshot(plan core.Plan, instance core.Instance, data *larkapproval.GetInstanceRespData) (core.ResultSnapshot, error) {
	result := core.ResultSnapshot{Kind: "workflow", Instance: instance, ModifiedInstanceID: resultText(data.ModifiedInstanceCode), RevertedInstanceID: resultText(data.RevertedInstanceCode)}
	var err error
	if result.StartedMS, err = nativeResultTime(data.StartTime); err != nil {
		return result, err
	}
	if result.CompletedMS, err = nativeResultTime(data.EndTime); err != nil {
		return result, err
	}
	if data.TaskList != nil {
		result.Tasks = []core.ResultTask{}
	}
	for _, task := range data.TaskList {
		if task == nil {
			return result, core.ErrConflict
		}
		entry := core.ResultTask{ID: resultText(task.Id), Actor: nativeResultActor(instance.TargetScope, task.OpenId), NodeID: resultText(task.NodeId), NodeName: resultText(task.NodeName), CustomNodeID: resultText(task.CustomNodeId)}
		entry.State = map[string]string{"PENDING": "pending", "APPROVED": "approved", "REJECTED": "rejected", "TRANSFERRED": "transferred", "DONE": "done"}[resultText(task.Status)]
		if entry.State == "" {
			entry.State = "unknown"
			entry.SourceState = resultText(task.Status)
		}
		entry.Kind = map[string]string{"AND": "all", "OR": "any", "SEQUENTIAL": "sequential", "AUTO_PASS": "auto_approve", "AUTO_REJECT": "auto_reject"}[resultText(task.Type)]
		if entry.Kind == "" {
			entry.Kind = "unknown"
			entry.SourceKind = resultText(task.Type)
		}
		if entry.StartedMS, err = nativeResultTime(task.StartTime); err != nil {
			return result, err
		}
		if entry.CompletedMS, err = nativeResultTime(task.EndTime); err != nil {
			return result, err
		}
		result.Tasks = append(result.Tasks, entry)
	}
	if data.CommentList != nil {
		result.Comments = []core.ResultComment{}
	}
	for _, comment := range data.CommentList {
		if comment == nil {
			return result, core.ErrConflict
		}
		entry := core.ResultComment{ID: resultText(comment.Id), Actor: nativeResultActor(instance.TargetScope, comment.OpenId), Text: resultText(comment.Comment)}
		if entry.AtMS, err = nativeResultTime(comment.CreateTime); err != nil {
			return result, err
		}
		if entry.Files, err = nativeResultFiles(comment.Files); err != nil {
			return result, err
		}
		result.Comments = append(result.Comments, entry)
	}
	if data.Timeline != nil {
		result.Actions = []core.ResultAction{}
	}
	for _, action := range data.Timeline {
		if action == nil {
			return result, core.ErrConflict
		}
		entry := core.ResultAction{Actor: nativeResultActor(instance.TargetScope, action.OpenId), TaskID: resultText(action.TaskId), NodeID: resultText(action.NodeKey), Comment: resultText(action.Comment)}
		entry.Kind = map[string]string{"START": "start", "PASS": "approve", "REJECT": "reject", "AUTO_PASS": "auto_approve", "AUTO_REJECT": "auto_reject", "REMOVE_REPEAT": "deduplicate", "TRANSFER": "transfer", "ADD_APPROVER_BEFORE": "add_before", "ADD_APPROVER": "add_parallel", "ADD_APPROVER_AFTER": "add_after", "DELETE_APPROVER": "remove_approver", "ROLLBACK_SELECTED": "rollback_selected", "ROLLBACK": "rollback", "CANCEL": "cancel", "DELETE": "delete", "CC": "copy"}[resultText(action.Type)]
		if entry.Kind == "" {
			entry.Kind = "unknown"
			entry.SourceKind = resultText(action.Type)
		}
		if entry.AtMS, err = nativeResultTime(action.CreateTime); err != nil {
			return result, err
		}
		if entry.Files, err = nativeResultFiles(action.Files); err != nil {
			return result, err
		}
		seen := map[string]bool{}
		add := func(id string) {
			if id != "" && !seen[id] {
				seen[id] = true
				entry.Recipients = append(entry.Recipients, core.Identity{Scope: instance.TargetScope, ID: id})
			}
		}
		for _, id := range action.OpenIdList {
			add(id)
		}
		for _, person := range action.CcUserList {
			if person == nil {
				return result, core.ErrConflict
			}
			add(resultText(person.OpenId))
		}
		if extension := resultText(action.Ext); extension != "" {
			var identities struct {
				ID  string   `json:"open_id"`
				IDs []string `json:"open_id_list"`
			}
			if json.Unmarshal([]byte(extension), &identities) != nil {
				return result, core.ErrConflict
			}
			entry.SourceMetadata = json.RawMessage(extension)
			add(identities.ID)
			for _, id := range identities.IDs {
				add(id)
			}
		}
		result.Actions = append(result.Actions, entry)
	}
	return core.NewResultSnapshot(plan, result)
}
