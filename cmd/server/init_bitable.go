package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/base"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/provision"
)

func runInitBitable(ctx context.Context, cfg config.Config, args []string, output io.Writer) error {
	if !cfg.FeishuEnabled() {
		return fmt.Errorf("init-bitable requires Feishu app credentials (app_id, app_secret) in config")
	}

	fs := flag.NewFlagSet("init-bitable", flag.ContinueOnError)
	fs.SetOutput(output)

	name := fs.String("name", "企业票据智能审核库", "Base name to create in Feishu")
	outputFile := fs.String("output", "configs/tables/enterprise.json", "Output path for the generated tables JSON profile")
	profileName := fs.String("profile", "enterprise", "Profile name in the generated tables JSON")
	folderToken := fs.String("folder-token", "", "Optional folder token to create the Base in")
	adminUser := fs.String("admin-user", "", "Optional user email or open_id to grant full_access admin permissions")
	transferOwner := fs.String("transfer-owner", "", "Optional user open_id or email to transfer Base ownership to")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	client := provision.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	svc := provision.NewService(client)

	fmt.Fprintf(output, "🚀 开始一键初始化多维表格: %s\n", *name)
	opts := provision.ProvisionOptions{
		BaseName:          *name,
		FolderToken:       *folderToken,
		AdminUser:         *adminUser,
		TransferOwnerUser: *transferOwner,
		OutputConfigFile:  *outputFile,
		ProfileName:       *profileName,
	}

	res, err := svc.ProvisionBase(ctx, opts)
	if err != nil {
		return fmt.Errorf("provisioning failed: %w", err)
	}

	fmt.Fprintf(output, "✅ 多维表格创建成功！\n")
	fmt.Fprintf(output, "   - Base 名称: %s\n", res.BaseName)
	fmt.Fprintf(output, "   - Base Token: %s\n", res.BaseToken)
	fmt.Fprintf(output, "   - 访问链接: %s\n", res.BaseURL)
	fmt.Fprintf(output, "   - 交易流水表 ID: %s\n", res.Tables[provision.RoleTransactions])
	fmt.Fprintf(output, "   - 个人报销明细 ID: %s\n", res.Tables[provision.RoleReimbursementDetails])
	fmt.Fprintf(output, "   - 发票台账 ID: %s\n", res.Tables[provision.RoleInvoiceLedger])
	fmt.Fprintf(output, "   - 跨表关联: 明细↔流水, 明细↔台账 双向关联已全部建立\n")
	fmt.Fprintf(output, "   - 权限状态: %s\n", res.AdvPermNote)

	if res.AdminAdded != "" {
		fmt.Fprintf(output, "   - 协作者设置: %s\n", res.AdminAdded)
	}
	if res.OwnerTransferred != "" {
		fmt.Fprintf(output, "   - 所有权设置: %s\n", res.OwnerTransferred)
	}

	fmt.Fprintf(output, "\n📄 反向生成表配置文件: %s\n", res.ConfigFile)

	// In-process self-check of the newly created Base schema
	fmt.Fprintf(output, "🔍 正在对新创建的多维表格执行闭环拓扑自验 (checkBusiness)...\n")
	tablesProfile, err := config.LoadTablesFile(res.ConfigFile)
	if err != nil {
		return fmt.Errorf("verify generated tables config: %w", err)
	}
	bizProfile := config.BusinessProfile{
		Version: tablesProfile.Version,
		Name:    tablesProfile.Name,
		Tables: config.BusinessTables{
			Transactions:         tablesProfile.Tables.Transactions,
			ReimbursementDetails: tablesProfile.Tables.ReimbursementDetails.TableBinding(),
			InvoiceLedger:        tablesProfile.Tables.InvoiceLedger,
		},
		Review: config.ReviewSettings{
			ContextFields: tablesProfile.Tables.ReimbursementDetails.ContextFields,
			ResultFields:  tablesProfile.Tables.ReimbursementDetails.ResultFields,
		},
	}
	ledgerClient := base.NewLedgerClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	var checkOut strings.Builder
	if err := checkBusiness(ctx, &bizProfile, ledgerClient, &checkOut); err != nil {
		return fmt.Errorf("self-check failed: %w", err)
	}
	fmt.Fprintf(output, "🎉 拓扑自验 100%% 通过！字段类型与双向关联完全符合系统运行预期。\n")
	fmt.Fprintf(output, "\n👉 下一步: 请在 configs/config.toml 中设置:\n   tables_file = %q\n", res.ConfigFile)

	return nil
}

func runAddAdmin(ctx context.Context, cfg config.Config, baseToken, userIdentifier string, output io.Writer) error {
	if !cfg.FeishuEnabled() {
		return fmt.Errorf("add-admin requires Feishu app credentials in config")
	}
	if strings.TrimSpace(baseToken) == "" || strings.TrimSpace(userIdentifier) == "" {
		return fmt.Errorf("usage: add-admin <base-token> <user-email-or-open-id>")
	}
	client := provision.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	svc := provision.NewService(client)
	if err := svc.AddAdmin(ctx, baseToken, userIdentifier); err != nil {
		return fmt.Errorf("add-admin failed: %w", err)
	}
	fmt.Fprintf(output, "✅ 已成功将 %s 添加为多维表格 %s 的管理员 (full_access)\n", userIdentifier, baseToken)
	return nil
}

func runTransferOwner(ctx context.Context, cfg config.Config, baseToken, userIdentifier string, output io.Writer) error {
	if !cfg.FeishuEnabled() {
		return fmt.Errorf("transfer-owner requires Feishu app credentials in config")
	}
	if strings.TrimSpace(baseToken) == "" || strings.TrimSpace(userIdentifier) == "" {
		return fmt.Errorf("usage: transfer-owner <base-token> <user-open-id>")
	}
	client := provision.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	svc := provision.NewService(client)
	if err := svc.TransferOwner(ctx, baseToken, userIdentifier); err != nil {
		return fmt.Errorf("transfer-owner failed: %w", err)
	}
	fmt.Fprintf(output, "✅ 已成功将多维表格 %s 的所有权转移给 %s\n", baseToken, userIdentifier)
	return nil
}
