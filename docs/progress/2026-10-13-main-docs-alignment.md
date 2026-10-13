# 2026-10-13 主仓库技术文档与代码对齐

## 范围与依据

用户在外层清理后明确指示“修改主仓库吧”。本轮对已发现的现状错误做文档一致性修复，起点为 `9000713`，工作区干净。以现有配置加载器、CLI 分发、审批服务、模型装配、Seal mapper、部署模板和包装器源码为依据，不将历史草案改成新的业务规则或实现授权。

本轮不新增业务能力，不修改配置字段、接口或工作流，不操作线上资源，不调整财务制度和员工提示词。AGENTS 的应用身份、收尾边界、审批前确认、提交检查及隐私要求保持不变；仅更正凭证字段来源和已有模块状态。

## 问题、证据与实际修订

- **配置入口错误**：`internal/config/config.go` 只通过 `CONFIG_FILE` 或默认 `configs/config.toml` 加载 TOML；旧文档仍描述环境变量凭证、根目录自动回退和 `BUSINESS_CONFIG_FILE`。AGENTS 更正凭证字段名，业务配置移除自动回退说明；运行交接增加当前操作说明，将旧配置正文保留为明确标注的历史快照。
- **审批归档范围混淆**：旧完整编排确实保存在 `archive/feishu-native-approval` / `archive/native-approval-20261008`，但 CLI 已提供单条 `preview-approval` 和 `push-approval`，服务最终调用 `CreateInstance`。AGENTS、主 README、飞书模块和审批说明改为当前轻量单向提单；旧 JSON、批次、附件准备、恢复及监听记录保留在归档历史段落，不再列为当前操作入口。
- **预览与开关描述过度**：审批预览会读取 Base，CLI 还会初始化本地审计／状态；`PushEnabled` 仅存在于配置结构，没有在 `push-approval` 调用链中拦截创建。当前说明不再承诺预览无网络／本地副作用，也不把 `push_enabled=false` 当作手动提单保护。本轮记录实际行为，没有修复或启用这个开关。
- **部署与模型配置过时**：Docker/K8s 使用 `/configs/config.toml`；模型识别与审核共用全局 `[model]`，规则文件优先按工作目录查找再回退主配置目录。相关文档更正旧 `/config.toml`、`review.model` 及独立识别连接写法；包装器的过时 JSON docstring 改为 TOML，执行语句保持不变。
- **Seal 模块说明滞后**：真实回调处理器已接入 `app/review`；当前 mapper 使用 `expense_details` 二维明细与 `EXPENSE_DETAIL_LIST`，结构化发票标识为 `feishu-invoice-<detailId>`。模块 README 与当前源码对齐，保留原件上传与缺失事实的既有边界。
- **历史文档假性“当前”**：公网调试、交付范围和旧审批下一阶段设计补日期与归档提示；旧交接和审批正文完整保留并降低标题层级，旧配置示例链接改为归档路径说明。前两份清理记录增加本次修订的后续链接，保留当时发现和验收原文。

涉及文件：

- `AGENTS.md`、`README.md`：现状入口、身份来源、当前模块／目录与历史审批边界。
- `docs/runtime-and-handoff.md`、`docs/business-configuration.md`、`docs/approval-configuration.md`：当前 TOML、路径解析、运行及提单操作。
- `docs/native-approval-implementation.md`、`docs/public-debug.md`、`docs/delivery-scope.md`：保留历史设计与部署证据，明确非当前运行证明。
- `internal/feishu/README.md`、`internal/anthropic/README.md`、`internal/seal/README.md`、`internal/seal/mapper/README.md`：模块现状。
- `deploy/run-local-debug.py`：仅文档字符串。
- `CHANGELOG.md`、本记录和前两份清理记录：修改追踪与阶段衔接。

## 行为与取舍

修改前，读者可能尝试旧配置、旧批次命令，或者认为主线完全没有原生审批。修改后，操作段落明确指向实际 TOML、当前单条 CLI 与源码边界；历史信息仍可追溯。服务的运行行为、部署版本及线上验收状态没有因为修改文档而改变。

采用追加当前说明和标注历史正文，避免丢失已有有效的联调证据。本轮没有删除文档，也没有改写归档分支或供应商接口原文。现有记录中的旧 JSON／环境变量名称可以继续出现，但只位于明确标注的历史段落中，不代表当前兼容能力。

## 验证与未覆盖事项

实际执行结果：

- `go test ./...` 与 `go test -tags no_anthropic ./...`：均通过，退出码 0，输出使用已有测试缓存。
- `go vet ./...`：通过，退出码 0。
- 从当前源码执行 `go run ./cmd/server help`，确认当前提单／审核命令存在，旧 `submit-seal`、批次准备／建单及审批事件订阅命令未列入帮助。帮助在加载配置前返回，未启动服务或操作线上资源。
- 17 个变更文件限定为技术文档、阶段记录与包装器 docstring；涉及文档的 112 个本地 Markdown 链接能解析。
- 当前 README 所列主要代码路径存在，旧模板入口、旧复杂审批目录和 `dedupe` 均不在主线。
- 将两份旧运行／审批正文与 HEAD 比对，除标题降低一层和失效示例链接改为历史路径说明外，原文完整保留。
- 将包装器修改前后 AST 去掉模块 docstring 后比较，完全相同，确认执行逻辑未变。
- `git diff --check` 通过；没有业务 Go 源码、依赖、配置文件、运行数据或员工提示词进入 Diff。

本轮完成后创建本地阶段提交，未推送远端。

未执行线上配置核验、OCR／Seal 审核、审批创建、通知、部署或监督进程重启。文档与源码一致不等于线上运行版本已更新。`push_enabled` 未拦截手动提单的代码行为仍保留，已在现行审批说明明确披露。

`SKILL.md` 和 `internal/skill/embedded/doubao_builtin_skill.md` 为面向员工的业务提示词，包含报销制度和流程描述，本轮不把技术文档核对扩展为财务规则或提示词行为修改，也不依据其中的“自动流转／打款”等文字认定服务已实现完整财务流程；这些内容仍需单独按实际制度与产品流程审核。因此本轮不声称整个仓库完全没有误导或所有业务说明均已验收。
