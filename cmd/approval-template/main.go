// Command approval-template explicitly creates a native Feishu approval template.
// It is a one-time setup tool and is not part of the running bridge service.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	larkapproval "github.com/larksuite/oapi-sdk-go/v3/service/approval/v4"
	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	app := flag.String("app", "", "credential set: bridge or approval (required)")
	apply := flag.Bool("apply", false, "create the template; without this flag only validate the request")
	flag.Parse()
	if (*app != "bridge" && *app != "approval") || flag.NArg() != 0 {
		return fmt.Errorf("usage: approval-template -app bridge|approval [-apply] (reads CONFIG_FILE or config.json)")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var definition larkapproval.ApprovalCreate
	if err := json.Unmarshal(cfg.ApprovalTemplate, &definition); err != nil {
		return fmt.Errorf("decode approval definition: %w", err)
	}
	if err := approval.ValidateNewDefinition(&definition); err != nil {
		return err
	}
	if !*apply {
		fmt.Printf("Validated new approval template for %s; no request sent.\n", *app)
		return nil
	}
	appID, appSecret := cfg.FeishuAppID, cfg.FeishuAppSecret
	if *app == "approval" {
		appID, appSecret = cfg.FeishuApprovalAppID, cfg.FeishuApprovalAppSecret
	}
	client, err := approval.NewDefinitionClient(appID, appSecret, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	created, err := client.CreateDefinition(ctx, &definition)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(created)
}
