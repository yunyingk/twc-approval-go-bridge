# 清理发票台账废弃审计列与字段定义精简

2026-10-09，Asia/Shanghai。针对发票台账（`invoice_ledger`）中历史残留但从未被系统写入的 5 个僵尸列（`audit_*` 审计规则字段）进行彻底清理与架构瘦身，恪守“自实现次要、以外部供应商事实为准”的极简原则。

## 背景与原因

在历史演进中，发票台账表配置了 5 个前缀为 `audit_` 的字段：
- `audit_confidence`（置信度）
- `audit_duplicate_flag`（重复标记）
- `audit_tips`（审核提示）
- `audit_whitelist_hit`（白名单命中）
- `audit_claim_status`（报销状态）

代码与线上数据审计确认：
1. **服务代码从不写入**：在 `internal/feishu/base/invoiceledger/ledger.go` 的台账持久化逻辑中，从未给这 5 个字段赋值，线上该列长期处于空置状态；
2. **职责归属错位**：单据机审结果（结论、建议、版本、外部单据ID等）已严格收敛回写到「个人报销明细表」，台账表只负责承载外部 OCR 票据客观事实；
3. **线上表结构虚挂**：线上发票台账真实表甚至未创建部分字段（如 `audit_confidence`），仅在本地配置中作为死配置存在。

## 改造内容

1. **配置模板清理**：
   - 从 `configs/tables/enterprise.example.json` 及测试配置中彻底移除上述 5 个字段定义；
   - 顺理 `bridge_source_key` 与 `bridge_raw_json` 的序号排序。
2. **代码逻辑收敛**：
   - `internal/feishu/base/invoiceledger/ledger.go`：移除废弃常量定义（`Confidence`, `DuplicateFlag`, `Tips`, `ReviewTips`, `WhitelistHit`, `ClaimStatus`）、移除 `normalizeLedgerSemantic` 中的 `"audit_"` 前缀识别，以及移除白名单校验。
   - `cmd/server/check_business.go`：移除针对废弃字段的特殊类型映射分支，保持类型检查干净严格。

## 验证与验收

- **单元测试与双构建全量通过**：
  - `go test ./...` 100% 通过；
  - `go test -tags no_anthropic ./...` 100% 通过；
  - `go vet ./...` 0 警告 0 错误。
- **二进制编译**：
  - `go build -o bin/twc-approval-go-bridge ./cmd/server` 编译无告警。
