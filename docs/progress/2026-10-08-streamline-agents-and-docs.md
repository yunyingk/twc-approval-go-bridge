# AGENTS.md 规则精简与全库文档状态对齐

## 实施背景

此前 `AGENTS.md` 经历了从 2026-10-03 到 2026-10-05 的多轮密集开发迭代，积累了 23 条带有具体日期的历史开发流水账。该状态导致了若干严重结构性问题与硬冲突：
1. **已归档代码在规则中假性存活**：包含大量关于已于 2026-10-08 剥离归档的原生审批扩展（13,300+ 行）实现细节；
2. **失效旧配置与当前架构冲突**：文中仍存有早期的 `.env` 查找指示和已废弃的 `BUSINESS_CONFIG_FILE` 路径说明；
3. **内容过度膨胀与重复**：关于 `configs/config.toml` 与 `configs/tables/` 的单一配置架构被重复叙述；
4. **README 同步滞后**：`README.md` 中仍留有审批初始化命令 `go run ./cmd/approval-template` 及 `preview-approval` 等已移除命令。

本阶段对 `AGENTS.md` 和 `README.md` 进行彻底核验与精简重构，确保项目规则与当前代码现状 100% 契合。

---

## 实施改动

1. **`AGENTS.md` 彻底精炼重构（从 16KB / 67 行流水账收敛为 4.4KB / 38 行高纯度规则）**：
   - **板块 1：范围与协作边界**：保留独立自包含仓库要求、结对编程、最小 Diff、阶段后提交、`docs/progress/` 记录过程、敏感信息防泄露，以及主线停止自动扩展的收尾红线；
   - **板块 2：飞书身份与配置规范**：保留强制使用应用身份（`tenant_access_token`，严禁用户 OAuth/扫码）、多维表格只读先行；确立以 `configs/config.toml` 为单一主配置、`configs/tables/*.json` 为业务拓扑，彻底剔除所有 `.env` 与 `BUSINESS_CONFIG_FILE` 历史失效描述；
   - **板块 3：当前主线架构与模块分工**：清晰定义小票识别（`internal/app/recognition`）、发票台账回写（`internal/feishu/base/invoiceledger`）、单据机审（`internal/app/review`）、多维表格数据源（`internal/feishu/base`）及核心契约（`internal/core`）；规范双构建测试命令（包含 `-tags no_anthropic`）；明确指出飞书原生审批已整体封存于 `archive/feishu-native-approval` 分支。
2. **清理本地残留空目录**：
   - 彻底删除本地磁盘上残留的未跟踪空目录 `internal/anyreceipt/flow` 与 `internal/anyreceipt/ledger`。
3. **`README.md` 审批章节同步归档标注**：
   - 将原「飞书审批模板初始化」及建单命令说明替换为清晰的归档状态声明，指向分支 `archive/feishu-native-approval`；
   - 在业务边界表格中将 `internal/feishu/approval/` 与 `internal/core/approval/`、`internal/app/approval/` 明确标记为已归档。

---

## 验证结论

- `go test ./...` 与 `go test -tags no_anthropic ./...` 全量单元测试通过；
- `go vet ./...` 检查无异常；
- `./bin/twc-approval-go-bridge check-business-config` 线上核验 100% 成功；
- 文档、配置与代码实现口径达成完全一致。
