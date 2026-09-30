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
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/approval"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	app := flag.String("app", "", "credential set: personal or enterprise (required)")
	file := flag.String("file", "", "JSON request body for a new approval definition (required)")
	apply := flag.Bool("apply", false, "create the template; without this flag only validate the request")
	flag.Parse()
	if (*app != "personal" && *app != "enterprise") || *file == "" || flag.NArg() != 0 {
		return fmt.Errorf("usage: approval-template -app personal|enterprise -file request.json [-apply]")
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read approval definition: %w", err)
	}
	var definition larkapproval.ApprovalCreate
	if err := json.Unmarshal(body, &definition); err != nil {
		return fmt.Errorf("decode approval definition: %w", err)
	}
	if err := approval.ValidateNewDefinition(&definition); err != nil {
		return err
	}
	if !*apply {
		fmt.Printf("Validated new approval template for %s; no request sent.\n", *app)
		return nil
	}
	prefix := "FEISHU_"
	if *app == "enterprise" {
		prefix = "FEISHU_APPROVAL_"
	}
	client, err := approval.NewDefinitionClient(os.Getenv(prefix+"APP_ID"), os.Getenv(prefix+"APP_SECRET"), nil)
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
