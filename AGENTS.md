# 项目协作规则

## 1. 范围与协作边界

- **独立自包含仓库**：本仓库 `twc-approval-go-bridge/` 为完全自包含的独立 Git 仓库。所有业务编排、配置模板（`configs/`）与设计文档（`docs/`）均完整内置于本项目，**严禁读取或依赖任何外部或上级目录**。项目级规则统一放在 `AGENTS.md`，技能使用 `.agents/skills`。
- **结对编程与最小 Diff**：结对编程，回答精炼；重大架构或业务改动先对齐方案，轻量修复可直接实施。遵循最小修改原则（最小 Diff），严格保留已有有效注释和文档。破坏性文件或危险 Git 操作前必须取得显式确认。
- **阶段验证与提交**：完成一个经验证的开发阶段后及时创建本地 Git commit，避免改动长期混在未提交的工作区中。
- **过程记录与隐私安全**：在 `docs/progress/` 保留各阶段的关键技术发现、实施选择与验收记录（随对应阶段提交）。**敏感密钥、完整私有载荷与运行凭据严禁提交 Git**。
- **红线与收尾边界**：历史文档中的“下一步”、T6 及财务后续事项不再作为自动推进授权。主线专注纯粹的海外小票 OCR 识别与 AI 审核回写；未获用户明确新指示前，不得自行推进人工审批、身份解析或财务流程。

## 2. 飞书身份与配置规范

- **应用身份（Tenant Access Token）**：本服务统一凭 `FEISHU_APP_ID` 与 `FEISHU_APP_SECRET` 以应用身份访问授权资源。**严禁发起用户 OAuth、要求用户扫码，或将 `lark-cli --as user` 当作服务权限验证**。
- **多维表格只读先行**：读取线上 Base/Table 时，以应用身份被授予的资源权限为准。先只读核验 Base、Table、字段及少量样本记录，不擅自切换为用户身份。变更事件需同时核对应用身份与用户身份 `bitable:app` 权限及 Base 云文档订阅。
- **单一主配置架构**：
  - 运行配置统一收敛于 `configs/config.toml`（0600 权限，受 `.gitignore` 保护，不入库；模板为 `configs/config.example.toml`）；
  - 业务数据表拓扑独立存放于 `configs/tables/*.json`（模板为 `configs/tables/enterprise.example.json`）；
  - 自有模型审核规则独立存放在 `configs/rules/`（SealAI 规则由外部维护，不加载本地规则）；
  - **彻底废弃 `.env` 与 `BUSINESS_CONFIG_FILE`**：配置加载仅通过 `configs/config.toml`（或环境变量 `CONFIG_FILE` 指定该文件路径），不合并旧环境变量或散落配置。

## 3. 当前主线架构与模块分工

- **核心流程职责**：
  - `internal/app/recognition`：小票识别编排层，支持飞书长连接变更事件与定时轮询发现新附件。提供方可选 Anyreceipt（`internal/anyreceipt`）或兼容模型（`internal/anthropic/model`）；
  - `internal/feishu/base/invoiceledger`：标准票据事实映射，将识别结果回写飞书「发票台账」并建立双向关联；
  - `internal/app/review`：单据机审编排层，支持识别完成自动送审（`after_recognition`）或手动送审。提供方可选 SealAI（`internal/seal`，支持 `base_url` + `webhook_id` 组装）或自有规则大模型（`internal/anthropic/review`）；审核结果回写明细表专用列；
  - `internal/feishu/base`：多维表格数据源适配（流水表只读、明细表读写、台账表读写）；
  - `internal/core/`：核心数据契约、中立票据结构（`invoice`）、审核结果模型（`review`）与台账查重（`dupcheck`）。
- **编译与验证约束**：
  - 默认构建包含 Anthropic SDK。轻量化构建支持使用编译标签：`go build -tags no_anthropic ./cmd/server`；
  - 代码改动后必须运行双构建测试：`go test ./...` 与 `go test -tags no_anthropic ./...`，以及 `go vet ./...`；
  - 涉及多维表格配置变更时，先运行 `./bin/twc-approval-go-bridge check-business-config` 进行线上只读核验。
- **原生审批代码归档状态**：
  - 飞书原生审批扩展功能（13,300+ 行）已整体封存至分支 `archive/feishu-native-approval`（标签 `archive/native-approval-20261008`），并从主线干净剥离。主线不包含原生审批客户端、表单与建单命令。
- **历史记录索引**：
  - 各历史阶段的详细演化脉络、联调日志与验收数据完整归档于 `docs/progress/` 目录，主规则不再保留逐日流水账。

