package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	native "github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
	"github.com/yunyingk/twc-approval-go-bridge/internal/state"
)

type approvalObservation struct {
	observer      *app.Observer
	lease         *os.File
	appID, secret string
	sink          events.Sink
	interval      time.Duration
}

func newApprovalObservation(cfg config.Config, logger *slog.Logger) (*approvalObservation, error) {
	if !cfg.ApprovalObservationEnabled() {
		return nil, nil
	}
	appID, secret, err := cfg.ApprovalObservationCredentials()
	if err != nil {
		return nil, err
	}
	if cfg.ReceiptBaseToken == "" || cfg.ReceiptTableID == "" {
		return nil, fmt.Errorf("approval observation requires an explicit source Base and Table")
	}
	interval := 5 * time.Minute
	if value := cfg.Business.Approval.Observation.PollInterval; value != "" {
		interval, err = time.ParseDuration(value)
		if err != nil || interval < time.Minute || interval > 24*time.Hour {
			return nil, fmt.Errorf("approval observation interval is invalid")
		}
	}
	client, err := native.NewInstanceClient(appID, secret, nil)
	if err != nil {
		return nil, err
	}
	lookup, err := native.NewInstanceLookupGateway(client)
	if err != nil {
		return nil, err
	}
	store, err := state.NewFiles(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("approval observation state is unavailable")
	}
	scope := "feishu:" + cfg.ReceiptBaseToken + ":" + cfg.ReceiptTableID
	observer, err := app.NewObserver(scope, lookup, store, logger)
	if err != nil {
		return nil, err
	}
	sink, err := events.NewApprovalSink(appID, scope, observer)
	if err != nil {
		return nil, err
	}
	lease, err := store.AcquireWorker("approval-observation:" + scope + ":" + lookup.TargetScope())
	if err != nil {
		return nil, fmt.Errorf("approval observation worker is already held or unavailable")
	}
	return &approvalObservation{observer, lease, appID, secret, sink.Sink, interval}, nil
}

type feishuRegistration struct {
	appID, secret string
	sinks         map[string]events.Sink
}

// Register each application once. Native events never flow through the Base
// raw-event logger, including when FEISHU_LOG_RAW_EVENTS is enabled.
func feishuRegistrations(cfg config.Config, baseSink events.Sink, observation *approvalObservation) ([]feishuRegistration, error) {
	registrations := []feishuRegistration{}
	if baseSink != nil {
		eventType := strings.TrimSpace(cfg.FeishuEventType)
		if eventType == "" {
			eventType = events.DefaultEventType
		}
		if eventType == events.ApprovalInstanceEventType {
			return nil, fmt.Errorf("Base event registration conflicts with native approval event")
		}
		registrations = append(registrations, feishuRegistration{cfg.FeishuAppID, cfg.FeishuAppSecret, map[string]events.Sink{eventType: baseSink}})
	}
	if observation != nil {
		if len(registrations) > 0 && registrations[0].appID == observation.appID {
			if registrations[0].secret != observation.secret {
				return nil, fmt.Errorf("shared Feishu event app has conflicting credentials")
			}
			registrations[0].sinks[events.ApprovalInstanceEventType] = observation.sink
		} else {
			registrations = append(registrations, feishuRegistration{observation.appID, observation.secret, map[string]events.Sink{events.ApprovalInstanceEventType: observation.sink}})
		}
	}
	return registrations, nil
}

func runApprovalSubscription(ctx context.Context, cfg config.Config, code string, output io.Writer) error {
	if !cfg.ApprovalObservationEnabled() {
		return fmt.Errorf("approval event subscription requires explicitly enabled observation")
	}
	if code == "" || code != strings.TrimSpace(code) || code != cfg.Business.Approval.TemplateCode {
		return fmt.Errorf("approval event subscription requires the configured explicit template code")
	}
	appID, secret, err := cfg.ApprovalObservationCredentials()
	if err != nil {
		return err
	}
	client, err := native.NewInstanceClient(appID, secret, nil)
	if err != nil {
		return err
	}
	return subscribeApprovalTemplate(ctx, client, code, output)
}

type approvalSubscriber interface {
	TargetScope() string
	GetDefinition(context.Context, string) (*larkapproval.GetApprovalRespData, error)
	SubscribeApprovalEvents(context.Context, string) error
}

func subscribeApprovalTemplate(ctx context.Context, client approvalSubscriber, code string, output io.Writer) error {
	definition, err := client.GetDefinition(ctx, code)
	if err != nil || definition == nil {
		return fmt.Errorf("approval subscription template is unavailable to the selected application")
	}
	if err := client.SubscribeApprovalEvents(ctx, code); err != nil {
		// Safe adapter errors retain HTTP/code diagnostics, never SDK messages.
		if failure, ok := err.(*native.InstanceAPIError); ok {
			return failure
		}
		return fmt.Errorf("approval event subscription transport or protocol failure; verify subscription before retrying")
	}
	return json.NewEncoder(output).Encode(struct {
		TargetScope           string `json:"target_scope"`
		Template              string `json:"template"`
		Subscription          string `json:"subscription"`
		EventDeliveryVerified bool   `json:"event_delivery_verified"`
	}{client.TargetScope(), code, "accepted", false})
}
