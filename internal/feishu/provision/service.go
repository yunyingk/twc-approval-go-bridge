package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
)

// ProvisionOptions customizes the Bitable provisioning process.
type ProvisionOptions struct {
	BaseName          string
	FolderToken       string
	AdminUser         string // email, open_id or user_id to grant full_access
	TransferOwnerUser string // email, open_id or user_id to transfer ownership
	OutputConfigFile  string // path to output json, e.g. configs/tables/enterprise.json
	ProfileName       string // profile name in json, e.g. enterprise
}

// ProvisionResult summarizes the output of the provisioning operation.
type ProvisionResult struct {
	BaseToken        string            `json:"base_token"`
	BaseURL          string            `json:"base_url"`
	BaseName         string            `json:"base_name"`
	Tables           map[string]string `json:"tables"` // role -> table_id
	ConfigFile       string            `json:"config_file,omitempty"`
	AdvPermActive    bool              `json:"adv_perm_active"`
	AdvPermNote      string            `json:"adv_perm_note,omitempty"`
	AdminAdded       string            `json:"admin_added,omitempty"`
	OwnerTransferred string            `json:"owner_transferred,omitempty"`
}

// Service orchestrates the end-to-end Bitable bootstrapping process.
type Service struct {
	client *Client
}

// NewService instantiates a provision Service.
func NewService(client *Client) *Service {
	return &Service{client: client}
}

// Client returns the underlying OpenAPI client.
func (s *Service) Client() *Client {
	return s.client
}

// ProvisionBase executes the entire bootstrapping workflow:
// 1. Creates Base in app root (or specified folder)
// 2. Enables advanced permissions (non-blocking on free edition)
// 3. Creates 3 standard tables (Transactions, ReimbursementDetails, InvoiceLedger)
// 4. Removes the initial default table created by Feishu
// 5. Creates cross-table relations (link fields)
// 6. Configures standard roles (gracefully degrades on free edition 1254304)
// 7. Optionally grants admin access or transfers ownership
// 8. Reverse-engineers and saves the standard configs/tables/*.json file.
func (s *Service) ProvisionBase(ctx context.Context, opts ProvisionOptions) (*ProvisionResult, error) {
	if strings.TrimSpace(opts.BaseName) == "" {
		opts.BaseName = "企业票据智能审核库"
	}
	if strings.TrimSpace(opts.ProfileName) == "" {
		opts.ProfileName = "enterprise"
	}
	if strings.TrimSpace(opts.OutputConfigFile) == "" {
		opts.OutputConfigFile = "configs/tables/enterprise.json"
	}

	bp := DefaultBlueprint()

	// 1. Create Base
	baseInfo, err := s.client.CreateBase(ctx, opts.BaseName, opts.FolderToken)
	if err != nil {
		return nil, fmt.Errorf("create base: %w", err)
	}

	result := &ProvisionResult{
		BaseToken: baseInfo.AppToken,
		BaseURL:   baseInfo.URL,
		BaseName:  baseInfo.Name,
		Tables:    make(map[string]string),
	}

	// 2. Enable Advanced Permissions (AdvPerm)
	if advErr := s.client.EnableAdvPerm(ctx, baseInfo.AppToken); advErr != nil {
		result.AdvPermActive = false
		result.AdvPermNote = fmt.Sprintf("开启高级权限提示: %v", advErr)
	} else {
		result.AdvPermActive = true
		result.AdvPermNote = "已成功开启高级权限 (is_advanced=true)"
	}

	// 3. Create Tables
	// 3.1 Transactions Table
	txReqFields := make([]TableFieldReq, 0, len(bp.TransactionsFields))
	for _, f := range bp.TransactionsFields {
		txReqFields = append(txReqFields, TableFieldReq{FieldName: f.Name, Type: f.Type, Property: f.Property})
	}
	txInfo, err := s.client.CreateTable(ctx, baseInfo.AppToken, "交易流水表", txReqFields)
	if err != nil {
		return nil, fmt.Errorf("create transactions table: %w", err)
	}
	result.Tables[RoleTransactions] = txInfo.TableID
	txFields := make(map[string]string, len(bp.TransactionsFields)+1)
	for i, f := range bp.TransactionsFields {
		if i < len(txInfo.FieldIDList) {
			txFields[f.SemanticKey] = txInfo.FieldIDList[i]
		}
	}

	// 3.2 Reimbursement Details Table
	allDetailsDefs := append([]FieldDef{}, bp.DetailsFields...)
	allDetailsDefs = append(allDetailsDefs, bp.DetailsResultCols...)
	dtReqFields := make([]TableFieldReq, 0, len(allDetailsDefs))
	for _, f := range allDetailsDefs {
		dtReqFields = append(dtReqFields, TableFieldReq{FieldName: f.Name, Type: f.Type, Property: f.Property})
	}
	dtInfo, err := s.client.CreateTable(ctx, baseInfo.AppToken, "个人报销明细", dtReqFields)
	if err != nil {
		return nil, fmt.Errorf("create reimbursement details table: %w", err)
	}
	result.Tables[RoleReimbursementDetails] = dtInfo.TableID
	dtFields := make(map[string]string)
	dtResultFields := make(map[string]string)
	for i, f := range bp.DetailsFields {
		if i < len(dtInfo.FieldIDList) {
			dtFields[f.SemanticKey] = dtInfo.FieldIDList[i]
		}
	}
	offset := len(bp.DetailsFields)
	for j, f := range bp.DetailsResultCols {
		idx := offset + j
		if idx < len(dtInfo.FieldIDList) {
			dtResultFields[f.SemanticKey] = dtInfo.FieldIDList[idx]
		}
	}

	// 3.3 Invoice Ledger Table
	lgReqFields := make([]TableFieldReq, 0, len(bp.LedgerOrderedCols))
	for _, f := range bp.LedgerOrderedCols {
		lgReqFields = append(lgReqFields, TableFieldReq{FieldName: f.Name, Type: f.Type, Property: f.Property})
	}
	lgInfo, err := s.client.CreateTable(ctx, baseInfo.AppToken, "发票台账", lgReqFields)
	if err != nil {
		return nil, fmt.Errorf("create invoice ledger table: %w", err)
	}
	result.Tables[RoleInvoiceLedger] = lgInfo.TableID
	lgOrderedFields := make([]config.OrderedField, 0, len(bp.LedgerOrderedCols)+1)
	for i, f := range bp.LedgerOrderedCols {
		if i < len(lgInfo.FieldIDList) {
			lgOrderedFields = append(lgOrderedFields, config.OrderedField{
				Order:  f.Order,
				Key:    f.SemanticKey,
				Name:   f.Name,
				Source: f.Source,
				ID:     lgInfo.FieldIDList[i],
			})
		}
	}

	// 4. Delete the initial default empty table created automatically by Feishu
	if baseInfo.DefaultTableID != "" {
		_ = s.client.DeleteTable(ctx, baseInfo.AppToken, baseInfo.DefaultTableID)
	}

	// 5. Create Link Relations
	// 5.1 Details -> Ledger (invoice_relation)
	invRelID, err := s.client.CreateField(ctx, baseInfo.AppToken, dtInfo.TableID, "关联发票台账", 18, map[string]any{
		"table_id": lgInfo.TableID,
		"multiple": true,
	})
	if err != nil {
		return nil, fmt.Errorf("create details->ledger relation field: %w", err)
	}
	dtFields["invoice_relation"] = invRelID

	// 5.2 Ledger -> Details (feishu_detail_relation, Order 30)
	detailRelID, err := s.client.CreateField(ctx, baseInfo.AppToken, lgInfo.TableID, "关联明细ID", 18, map[string]any{
		"table_id": dtInfo.TableID,
		"multiple": true,
	})
	if err != nil {
		return nil, fmt.Errorf("create ledger->details relation field: %w", err)
	}
	lgOrderedFields = append(lgOrderedFields, config.OrderedField{
		Order:  30,
		Key:    "feishu_detail_relation",
		Name:   "关联明细ID",
		Source: "feishu_relation",
		ID:     detailRelID,
	})
	// Keep ordered fields sorted by Order ascending
	sort.Slice(lgOrderedFields, func(i, j int) bool {
		return lgOrderedFields[i].Order < lgOrderedFields[j].Order
	})

	// 5.3 Details -> Transactions (transaction_relation)
	txRelID, err := s.client.CreateField(ctx, baseInfo.AppToken, dtInfo.TableID, "关联交易流水", 18, map[string]any{
		"table_id": txInfo.TableID,
		"multiple": true,
	})
	if err != nil {
		return nil, fmt.Errorf("create details->transactions relation field: %w", err)
	}
	dtFields["transaction_relation"] = txRelID

	// 5.4 Transactions -> Details (detail_relation)
	dtBackRelID, err := s.client.CreateField(ctx, baseInfo.AppToken, txInfo.TableID, "关联报销明细", 18, map[string]any{
		"table_id": dtInfo.TableID,
		"multiple": true,
	})
	if err != nil {
		return nil, fmt.Errorf("create transactions->details relation field: %w", err)
	}
	txFields["detail_relation"] = dtBackRelID

	// 6. Setup Standard Role ("消费流水人员")
	if result.AdvPermActive {
		tableRoles := []TableRoleReq{
			{TableID: dtInfo.TableID, TablePerm: 2, AllowAddRecord: false, AllowDeleteRecord: false}, // 明细表可编辑
			{TableID: txInfo.TableID, TablePerm: 0},                                                    // 流水表隐藏
			{TableID: lgInfo.TableID, TablePerm: 0},                                                    // 台账表隐藏
		}
		_, roleErr := s.client.CreateRole(ctx, baseInfo.AppToken, "消费流水人员", tableRoles)
		if roleErr != nil {
			errStr := roleErr.Error()
			if strings.Contains(errStr, "1254304") || strings.Contains(errStr, "Business and Enterprise Editions") {
				result.AdvPermNote += "；检测到当前企业为飞书基础版，已安全降级为标准模式（跳过增值版角色配置）"
			} else {
				result.AdvPermNote += fmt.Sprintf("；角色创建提示: %v", roleErr)
			}
		} else {
			result.AdvPermNote += "；已配置标准角色「消费流水人员」"
		}
	}

	// 7. Add Administrator Collaborator if specified
	if strings.TrimSpace(opts.AdminUser) != "" {
		mType := detectMemberType(opts.AdminUser)
		if err := s.client.AddCollaborator(ctx, baseInfo.AppToken, mType, opts.AdminUser, "full_access"); err != nil {
			result.AdminAdded = fmt.Sprintf("添加管理员失败: %v", err)
		} else {
			result.AdminAdded = fmt.Sprintf("已成功添加管理员 %s (%s, full_access)", opts.AdminUser, mType)
		}
	}

	// 8. Transfer Ownership if specified
	if strings.TrimSpace(opts.TransferOwnerUser) != "" {
		mType := detectMemberType(opts.TransferOwnerUser)
		if err := s.client.TransferOwner(ctx, baseInfo.AppToken, mType, opts.TransferOwnerUser); err != nil {
			result.OwnerTransferred = fmt.Sprintf("转移所有者失败: %v", err)
		} else {
			result.OwnerTransferred = fmt.Sprintf("已将所有者转移给 %s (%s)", opts.TransferOwnerUser, mType)
		}
	}

	// 9. Assemble TablesProfile & Save JSON
	profile := config.TablesProfile{
		Version: 1,
		Name:    opts.ProfileName,
		Tables: config.TablesSchema{
			Transactions: config.TableBinding{
				Name:      "交易流水表",
				Source:    "third_party_webhook",
				Access:    "read_only",
				BaseToken: baseInfo.AppToken,
				TableID:   txInfo.TableID,
				Fields:    txFields,
			},
			ReimbursementDetails: config.ReimbursementDetailsBinding{
				Name:      "个人报销明细",
				Source:    "employee",
				Access:    "read_write",
				BaseToken: baseInfo.AppToken,
				TableID:   dtInfo.TableID,
				Fields:    dtFields,
				ContextFields: map[string]string{
					"expense_reason":   dtFields["expense_reason"],
					"expense_category": dtFields["expense_category"],
				},
				ResultFields: dtResultFields,
			},
			InvoiceLedger: config.TableBinding{
				Name:          "发票台账",
				Source:        "bridge",
				Access:        "read_write",
				BaseToken:     baseInfo.AppToken,
				TableID:       lgInfo.TableID,
				OrderedFields: lgOrderedFields,
			},
		},
	}

	if err := saveTablesJSON(opts.OutputConfigFile, profile); err != nil {
		return nil, fmt.Errorf("save tables json: %w", err)
	}
	result.ConfigFile = opts.OutputConfigFile

	return result, nil
}

// AddAdmin grants full_access to the specified user on the Base.
func (s *Service) AddAdmin(ctx context.Context, baseToken, userIdentifier string) error {
	mType := detectMemberType(userIdentifier)
	return s.client.AddCollaborator(ctx, baseToken, mType, userIdentifier, "full_access")
}

// TransferOwner transfers Base ownership to the specified user.
func (s *Service) TransferOwner(ctx context.Context, baseToken, userIdentifier string) error {
	mType := detectMemberType(userIdentifier)
	return s.client.TransferOwner(ctx, baseToken, mType, userIdentifier)
}

func detectMemberType(id string) string {
	id = strings.TrimSpace(id)
	switch {
	case strings.Contains(id, "@"):
		return "email"
	case strings.HasPrefix(id, "ou_"):
		return "openid"
	case strings.HasPrefix(id, "oc_"):
		return "openchat"
	default:
		return "userid"
	}
}

func saveTablesJSON(path string, profile config.TablesProfile) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tables json: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}
