# 原生人工审批扩展代码归档与主线剥离

2026-10-08，Asia/Shanghai。用户确认执行方案 A：将历史上由智能体自主推演出的飞书原生审批扩展（未启用、未真实验收）完整归档并从主线剥离，恢复 OCR 识别与 AI 审核主线链路的纯粹性与可维护性。

## 归档标识与位置

- 完整保留分支：`archive/feishu-native-approval`
- 永久快照标签：`archive/native-approval-20261008`
- 对应基线提交：`03b9371`

后续若业务正式启动飞书原生审批项目，可随时通过上述分支或标签恢复全部 13,000+ 行实现代码。

## 剥离范围（66 个文件，13,339 行代码）

1. **入口与命令**：
   - 移除独立模板工具 `cmd/approval-template/`；
   - 移除 `cmd/server/` 中的 12 个审批代码与测试文件（`prepare_approval*`、`preview_approval*`、`submit_approval*`、`approval_observation*` 等）；
   - 从 `cmd/server/main.go` 移除 9 个审批专用子命令（`preview-approval`, `prepare-approval`, `submit-approval`, `retry-approval`, `approval-status`, `check-approval`, `abandon-approval`, `subscribe-approval-events`, `retry-approval-files`）；
   - 移除 `main.go` 中的后台审批观察者与长连接合并注册，回归直接注册多维表格变更事件。
2. **核心域与编排**：
   - 移除 `internal/core/approval/`（实体、计划、预约、尝试、结果）；
   - 移除 `internal/app/approval/`（业务流、批次、准备、重试、观察者）。
3. **飞书适配与横向污染**：
   - 移除 `internal/feishu/approval/`（原生审批 SDK、实例、网关、表单映射）；
   - 移除 `internal/feishu/base/approval_source.go` 及 `approval_values.go`；
   - 移除 `internal/feishu/events/approval.go`。
4. **状态持久化**：
   - 移除 `internal/state/` 中 6 个审批专用状态文件（`approval.go`, `approval_batch.go`, `approval_preparation.go`, `approval_upload.go`, `approval_notices.go`, `approval_unsent.go`）。
5. **配置解耦**：
   - 移除 `internal/config/approval.go`；
   - 从 `internal/config/business.go` 和 `config.go` 移除 `Approval` 字段及其校验；
   - 从 `configs/config.example.toml` 移除废弃的 `feishu.approval_app` 与 `[approval]` 块；
   - 移除 `templates/feishu/approval-template.example.json`。

## 验证与验收

- `go test ./...` 与 `go test -tags no_anthropic ./...`：全量测试 100% 通过；
- `go vet ./...` 与 `go vet -tags no_anthropic ./...`：全量检查 100% 通过；
- `go build ./cmd/server`：正常构建成功；
- 真实配置核验：`go run ./cmd/server check-business-config` 读取真实 `config.toml` 成功输出三张业务表信息；
- 源码体量变化：从 24,486 行骤降至 11,147 行，减重 54.4%。主线业务（Anyreceipt/Seal/Bitable）完全不受影响。
