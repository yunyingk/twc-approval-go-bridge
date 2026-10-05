package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

const ApprovalInstanceEventType = "approval_instance"

type ApprovalNoticeConsumer interface {
	Enqueue(context.Context, core.Notice) error
}
type ApprovalSink struct {
	appID, source string
	consumer      ApprovalNoticeConsumer
}

func NewApprovalSink(appID, source string, consumer ApprovalNoticeConsumer) (*ApprovalSink, error) {
	if appID == "" || source == "" || consumer == nil {
		return nil, fmt.Errorf("approval sink requires explicit app, source and durable consumer")
	}
	return &ApprovalSink{appID, source, consumer}, nil
}

// Sink handles the verified 1.0 native event contract, not task/person-scoped v4
// events. Top-level uuid is the event ID; event.uuid is the creation UUID. Unknown
// status/time fields are deliberately not interpreted as authoritative decisions.
func (s *ApprovalSink) Sink(ctx context.Context, incoming Event) error {
	if incoming.Type != ApprovalInstanceEventType {
		return nil
	}
	var body struct {
		UUID  string `json:"uuid"`
		Type  string `json:"type"`
		Event struct {
			AppID      string `json:"app_id"`
			Type       string `json:"type"`
			Template   string `json:"approval_code"`
			InstanceID string `json:"instance_code"`
			PlanID     string `json:"uuid"`
		} `json:"event"`
	}
	if json.Unmarshal(incoming.Payload, &body) != nil || body.Type != "event_callback" || body.Event.Type != ApprovalInstanceEventType {
		return fmt.Errorf("invalid native approval event envelope")
	}
	if body.Event.AppID != s.appID {
		return nil
	}
	if incoming.ID != body.UUID {
		return fmt.Errorf("native approval event ID conflicts with transport")
	}
	n := core.Notice{ID: body.UUID, SourceScope: s.source, TargetScope: "feishu-app:" + s.appID, Template: body.Event.Template, InstanceID: body.Event.InstanceID, PlanID: body.Event.PlanID}
	if err := n.Validate(); err != nil {
		return fmt.Errorf("invalid native approval lookup identities")
	}
	// Leave room for the SDK acknowledgement deadline. Network lookups occur
	// only in the background after successful local persistence.
	queueCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.consumer.Enqueue(queueCtx, n); err != nil {
		return fmt.Errorf("persist native approval notice failed")
	}
	return nil
}
