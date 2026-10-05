package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

type noticeConsumer func(context.Context, core.Notice) error

func (f noticeConsumer) Enqueue(ctx context.Context, n core.Notice) error { return f(ctx, n) }

func TestSDKDispatcherRoutesP1ApprovalAndP2BaseWithoutMixingUUIDsOrPayloads(t *testing.T) {
	ctx := context.Background()
	baseCalls, approvalCalls := 0, 0
	sink, _ := NewApprovalSink("app", "source", noticeConsumer(func(ctx context.Context, n core.Notice) error {
		approvalCalls++
		if n.ID != "event-id" || n.PlanID != "creation-uuid" || n.InstanceID != "native-code" || n.TargetScope != "feishu-app:app" || n.Template != "template" {
			t.Fatal("event identity mixed with creation UUID")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("event queue lacks acknowledgement deadline")
		}
		raw, _ := json.Marshal(n)
		if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "APPROVED") {
			t.Fatal("token/claimed decision entered durable trigger")
		}
		return nil
	}))
	dispatcher, types, err := newEventDispatcher(map[string]Sink{
		ApprovalInstanceEventType: sink.Sink,
		DefaultEventType: func(_ context.Context, event Event) error {
			baseCalls++
			if event.ID != "base-event" || event.Type != DefaultEventType {
				t.Fatal("Base V2 header was lost")
			}
			return nil
		},
	})
	if err != nil || len(types) != 2 {
		t.Fatal(err)
	}
	// Do is the SDK's persistent-connection dispatch path; authentication is at
	// the app connection, so this is not a public HTTP callback handler.
	for _, payload := range []string{
		`{"uuid":"event-id","type":"event_callback","token":"PRIVATE_TOKEN","event":{"type":"approval_instance","app_id":"app","approval_code":"template","instance_code":"native-code","uuid":"creation-uuid","status":"APPROVED","operate_time":1791100000000}}`,
		`{"schema":"2.0","header":{"event_id":"base-event","event_type":"drive.file.bitable_record_changed_v1"},"event":{}}`,
	} {
		if _, err := dispatcher.Do(ctx, []byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	if baseCalls != 1 || approvalCalls != 1 {
		t.Fatal("two registered event formats crossed sinks")
	}
	other := []byte(`{"uuid":"event-id","type":"event_callback","event":{"type":"approval_instance","app_id":"other","approval_code":"template","instance_code":"native-code"}}`)
	if _, err := dispatcher.Do(ctx, other); err != nil || approvalCalls != 1 {
		t.Fatal("foreign app event consumed")
	}
}

func TestNativeApprovalSinkRejectsMalformedBindingAndPropagatesPersistenceFailureSafely(t *testing.T) {
	calls := 0
	sink, _ := NewApprovalSink("app", "source", noticeConsumer(func(context.Context, core.Notice) error { calls++; return errors.New("PRIVATE_DISK_ERROR") }))
	for _, body := range []string{`{`, `{"type":"event_callback","uuid":"event","event":{"app_id":"app","type":"approval_instance"}}`, `{"type":"event_callback","uuid":"event","event":{"app_id":"app","type":"other"}}`} {
		incoming := decodeEvent([]byte(body), ApprovalInstanceEventType)
		incoming.Type = ApprovalInstanceEventType
		err := sink.Sink(context.Background(), incoming)
		if err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("malformed event accepted or leaked")
		}
	}
	if calls != 0 {
		t.Fatal("malformed binding reached state")
	}
	valid := decodeEvent([]byte(`{"type":"event_callback","uuid":"event","event":{"app_id":"app","type":"approval_instance","approval_code":"template","instance_code":"instance"}}`), ApprovalInstanceEventType)
	bad := valid
	bad.ID = "creation-uuid"
	if err := sink.Sink(context.Background(), bad); err == nil || calls != 0 {
		t.Fatal("conflicting transport ID accepted")
	}
	if err := sink.Sink(context.Background(), valid); err == nil || strings.Contains(err.Error(), "PRIVATE") || calls != 1 {
		t.Fatal("disk error not safely propagated to SDK acknowledgement")
	}
	for _, registrations := range []map[string]Sink{nil, {"app_ticket": nil}, {" approval_instance ": nil}} {
		if _, _, err := newEventDispatcher(registrations); err == nil {
			t.Fatal("reserved or invalid registration accepted")
		}
	}
}
