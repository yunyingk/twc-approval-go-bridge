# 业务多维表格拓扑与系统凭证彻底解耦

2026-10-08，Asia/Shanghai。针对项目历史上将系统级最高敏感资产（Feishu AppSecret、Anyreceipt APIKey、Seal BearerToken）与数十个易变的飞书业务字段 ID（fldXXX）强行捆绑在单一配置文件，且将报销明细表人为撕裂为三个小节（常规字段、审核上下文、AI 结果回写）的结构性痛点，完成业务拓扑抽取与配置体系重构。

## 核心设计与改造要点

1. **凭证资产与业务 Schema 物理隔离**：
   - 敏感凭据留在私有 `config.toml`（0600 权限，Git 忽略），负责服务生命周期、端口日志、飞书应用身份与供应商鉴权。
   - 三张业务数据表（交易流水表、个人报销明细表、发票台账）的 Base/Table ID 与列映射独立拆入 `configs/tables/`（私有文件为 `enterprise-test.json`，脱敏模板为 `enterprise.example.json`）。
   - `config.toml` 通过 `tables_file = "configs/tables/enterprise-test.json"` 显式引用数据表拓扑，彻底杜绝环境变量隐式覆盖。
2. **终结“语义孤岛”，报销明细表集中定义**：
   - 在新数据表 JSON 中，`reimbursement_details` 将常规字段（`fields`）、机审上下文列（`context_fields`）和 AI 回写列（`result_fields`）三合一集中声明，便于业务与运维人员完整盘点明细表结构。
3. **内存无缝组装，零破坏下游业务**：
   - `internal/config.LoadFile` 解析 TOML 与 JSON 后，无缝组装回 `BusinessProfile` 与 `Config` 结构体。
   - `cmd/server/check_business.go`、`internal/app/review`、`internal/app/recognition` 等所有核心业务层零代码修改，实现完全透明解耦。
4. **严格校验与防御性加载**：
   - `LoadTablesFile` 与 `LoadFile` 均开启 `DisallowUnknownFields`，防止旧格式或未知字段静默混入。
   - 保持完整的业务约束校验（同 Base 约束、字段冲突检查、保留关键字校验等）。

## 验证与验收

- **单元测试**：
  - 新增 `TestLoadTablesFile_Valid`、`TestLoadTablesFile_DisallowsUnknownFields`、`TestLoadFile_RequiresTablesFile`、`TestLoadFile_RejectsTablesInTOML` 等专项测试；
  - `go test -v ./internal/config` 21 项测试全部通过；
  - `go test ./...` 与 `go test -tags no_anthropic ./...` 双构建模式全量测试 100% 通过；
  - `go vet ./...` 检查 100% 通过。
- **真机业务核验**：
  - 编译二进制：`go build -o bin/twc-approval-go-bridge ./cmd/server`；
  - 执行 `./bin/twc-approval-go-bridge check-business-config`，成功读取 `config.toml` + `enterprise-test.json`，使用真实飞书应用身份核验多维表格结构：
    - `transactions`: 14 个字段通过校验；
    - `reimbursement_details`: 20 个字段通过校验（含 2 个上下文列与 7 个 AI 回写专用列）；
    - `invoice_ledger`: 18 个字段通过校验。
  - `-tags no_anthropic` 二进制验证同样通过。
