package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/events"
)

func observerConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{StateDir: t.TempDir(), ReceiptBaseToken: "base", ReceiptTableID: "details", FeishuAppID: "bridge", FeishuAppSecret: "secret", FeishuApprovalAppID: "approval", FeishuApprovalAppSecret: "approval-secret", Business: &config.BusinessProfile{Approval: &config.ApprovalSettings{Mode: "disabled", TargetIdentity: "bridge", TemplateCode: "template", Observation: &config.ApprovalObservation{Enabled: true}}}}
}

func TestObservationAssemblyDoesNotRequireCreationAndEnforcesOneWorker(t *testing.T) {
	cfg := observerConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker, err := newApprovalObservation(cfg, logger)
	if err != nil || worker == nil || worker.interval != 5*time.Minute {
		t.Fatal("disabled creation could not observe original app")
	}
	defer worker.lease.Close()
	if err := worker.observer.Poll(context.Background()); err != nil {
		t.Fatal("empty observer performed network lookup")
	}
	if _, err := newApprovalObservation(cfg, logger); err == nil {
		t.Fatal("second local observer claimed same source/app")
	}
	worker.lease.Close()
	worker, err = newApprovalObservation(cfg, logger)
	if err != nil {
		t.Fatal("worker did not recover after releasing OS lock")
	}
	worker.lease.Close()
	cfg.Business.Approval.Observation.Enabled = false
	cfg.StateDir = filepath.Join(t.TempDir(), "not-created")
	if worker, err := newApprovalObservation(cfg, logger); err != nil || worker != nil {
		t.Fatal("inactive observation was assembled")
	}
	if _, err := os.Stat(cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("inactive observation created state")
	}
}

func TestFeishuRegistrationsShareSameAppAndSeparateIndependentApp(t *testing.T) {
	cfg := observerConfig(t)
	baseCalls, nativeCalls := 0, 0
	baseSink := events.Sink(func(context.Context, events.Event) error { baseCalls++; return nil })
	observation := &approvalObservation{appID: cfg.FeishuAppID, secret: cfg.FeishuAppSecret, sink: func(context.Context, events.Event) error { nativeCalls++; return nil }}
	registrations, err := feishuRegistrations(cfg, baseSink, observation)
	if err != nil || len(registrations) != 1 || len(registrations[0].sinks) != 2 {
		t.Fatal("same application started duplicate connections")
	}
	registrations[0].sinks[events.ApprovalInstanceEventType](context.Background(), events.Event{})
	if nativeCalls != 1 || baseCalls != 0 {
		t.Fatal("native event entered Base raw logger")
	}
	observation.appID, observation.secret = cfg.FeishuApprovalAppID, cfg.FeishuApprovalAppSecret
	registrations, err = feishuRegistrations(cfg, baseSink, observation)
	if err != nil || len(registrations) != 2 || registrations[1].appID != cfg.FeishuApprovalAppID || len(registrations[1].sinks) != 1 {
		t.Fatal("separate application fell back to bridge")
	}
	if registrations, err = feishuRegistrations(cfg, nil, observation); err != nil || len(registrations) != 1 || registrations[0].appID != observation.appID {
		t.Fatal("poll-only Base prevented approval connection")
	}
	cfg.FeishuEventType = events.ApprovalInstanceEventType
	if _, err := feishuRegistrations(cfg, baseSink, observation); err == nil {
		t.Fatal("native event registration silently replaced Base sink")
	}
}

type fakeSubscriber struct {
	calls                []string
	getErr, subscribeErr error
}

func (*fakeSubscriber) TargetScope() string { return "feishu-app:chosen" }
func (s *fakeSubscriber) GetDefinition(_ context.Context, code string) (*larkapproval.GetApprovalRespData, error) {
	s.calls = append(s.calls, "get:"+code)
	return &larkapproval.GetApprovalRespData{}, s.getErr
}
func (s *fakeSubscriber) SubscribeApprovalEvents(_ context.Context, code string) error {
	s.calls = append(s.calls, "subscribe:"+code)
	return s.subscribeErr
}

func TestApprovalSubscriptionCommandGuardsAndDoesNotClaimEventDelivery(t *testing.T) {
	ctx := context.Background()
	cfg := observerConfig(t)
	cfg.StateDir = filepath.Join(t.TempDir(), "untouched")
	for _, change := range []string{"disabled", "wrong_template", "missing_secret", "ambiguous_credentials"} {
		t.Run(change, func(t *testing.T) {
			c := observerConfig(t)
			c.StateDir = cfg.StateDir
			code := "template"
			switch change {
			case "disabled":
				c.Business.Approval.Observation.Enabled = false
			case "wrong_template":
				code = "other"
			case "missing_secret":
				c.FeishuAppSecret = ""
			case "ambiguous_credentials":
				c.FeishuApprovalAppID = c.FeishuAppID
			}
			var output bytes.Buffer
			if err := runApprovalSubscription(ctx, c, code, &output); err == nil || output.Len() != 0 {
				t.Fatal("subscription guard allowed external call")
			}
		})
	}
	if _, err := os.Stat(cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("subscription guard wrote local state")
	}
	for _, scenario := range []string{"success", "read_failed", "subscribe_failed"} {
		s := &fakeSubscriber{}
		if scenario == "read_failed" {
			s.getErr = errors.New("PRIVATE")
		}
		if scenario == "subscribe_failed" {
			s.subscribeErr = errors.New("PRIVATE")
		}
		var output bytes.Buffer
		err := subscribeApprovalTemplate(ctx, s, "template", &output)
		if scenario == "success" {
			if err != nil || !strings.Contains(output.String(), `"event_delivery_verified":false`) || len(s.calls) != 2 || s.calls[0] != "get:template" {
				t.Fatal("subscribe did not read before writing or claimed live event acceptance")
			}
		} else if err == nil || strings.Contains(err.Error(), "PRIVATE") || output.Len() != 0 {
			t.Fatal("subscription failure was hidden or leaked")
		}
		if scenario == "read_failed" && len(s.calls) != 1 {
			t.Fatal("unreadable template was subscribed")
		}
	}
}
