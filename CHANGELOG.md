# 变更日志 (CHANGELOG)

本项目的所有显著变更均记录于此文件中。

本日志格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/) 规范，版本号遵循 [语义化版本 2.0.0 (Semantic Versioning)](https://semver.org/lang/zh-CN/)。

---

## 维护规范与修改追踪机制

为保证**每一个修改有迹可循**，本项目实施以下变更登记制度：

1. **未发布变更登记 (`[Unreleased]`)**：
   - 任何涉及功能增减、行为调整、配置变动、缺陷修复或安全补丁的提交，**必须**在 `[Unreleased]` 对应的分类小节下登记一句话说明与影响模块；
   - 严禁隐式或未经记录的代码逻辑变更。

2. **变更类型分类**：
   - **新增 (Added)**：新功能、新 CLI 命令、新接口或新配置项；
   - **变更 (Changed)**：现有功能的优化、重构或行为调整；
   - **弃用 (Deprecated)**：即将废弃但不立即移除的旧功能/字段；
   - **移除 (Removed)**：已彻底清理或移除的功能/代码；
   - **修复 (Fixed)**：缺陷修复、并发安全修复或数据兼容性修复；
   - **安全 (Security)**：安全漏洞修复、敏感信息保护或访问权限强化。

3. **版本发布归档**：
   - 当准备发布新版本时，将 `[Unreleased]` 中的条目冻结为新版本号章节（例如 `[0.4.0] - YYYY-MM-DD`），并重新保留一个空的 `[Unreleased]` 模板；
   - 同步更新 `internal/version/version.go` 中的版本号，并通过 `git tag -a vX.Y.Z -m "Release vX.Y.Z"` 创建 Git 标签。

---

## [Unreleased]

### 新增 (Added)
- **飞书原生审批单向推送模块恢复 (`internal/feishu/approval`)**：
  - 重新恢复接入飞书原生审批提单能力，严格遵循“只推送、不回调、无反向监听”的单向轻量 Dispatcher 边界；
  - 完整适配当前企业已生效的「海外易商卡消费确认单」模板（`7B19DB2B-1495-4CD8-A723-881F3859EBCE`），覆盖 3 个顶层表单控件与 20 个明细子控件，映射流水、发票事实及 AI 机审结果；
  - 引入 `CreateInstanceRequest` 构造器与确定性 UUID 幂等防重机制，支持指定节点自选审批人；
  - 扩展 CLI 运维子命令 `preview-approval <id>`（只读预览 Dry-Run）与 `push-approval <id>`（单向推单）；
  - 配置层支持 `[feishu.approval]` 集中管理模板 Code 与默认审批人。

### 变更 (Changed)
- **SealAI 提审字段映射全面对齐《飞书表单推送Sealai字段映射方案》规范 (`internal/seal`)**：
  - 将多发票明细由单层前缀平铺全面重构为标准二维嵌套明细表（`key: "expense_details"`, `type: "LIST"`, `semanticType: "EXPENSE_DETAIL_LIST"`），每行包含发票台账 24 列业务事实及必需的 `detailId`；
  - 金额字段（`item_amount`、`transaction_amount`、`settlement_amount`）从标量文本升级为强类型金额结构体 `{"amount": ..., "currency": ...}`（`type: "AMOUNT"`）；
  - 顶层单据主体字段完整对齐 24 列单值项（覆盖易商卡推送 15 列、报销明细 6 列、项目关联等），严格遵守“空单元格保留字段项传空字符串”规范；
  - 结构化发票数组 `invoices` 补全 `claimedAmount` 与 `expenseRefs`（包含 `sourceExpenseId` 关联发票台账记录 ID），`sourceInvoiceId` 统一对齐 `feishu-invoice-<record_id>` 规范；
  - 购买方补全规则：当 OCR 未识别购买方时，支持安全回退至唯一报销人姓名。

### 修复 (Fixed)
- 暂无

---

## [0.3.3] - 2026-10-12

### 运维监控升级、飞书Bot交互与看板优化 (Ops Monitoring, Feishu Bot & Dashboard Enhancements)

### 新增 (Added)
- **健康探测补充 IM 权限全量核验 (`internal/health/checker.go`)**：在 `doctor` 诊断工具中补充对「机器人发消息权限」(im:message:send_as_bot)、「私信单聊读取权限」(im:message.p2p_msg:readonly) 以及「长连接事件订阅配置」的真实探测项，杜绝权限遗漏导致诊断虚假 100% HEALTHY。
- **飞书对话响应式交互系统与机器人应答 (`internal/feishu/bot`)**：实现基于飞书长连接（`im.message.receive_v1`）的企业机器人智能响应系统。支持私信单聊免@直接交互与群聊@机器人交互，内置意图分类器，精准识别「管理页面 / 看板」、「系统状态」、「帮助指引」等指令，具备严格的群聊防剧透安全策略与 5 分钟去重防抖防护。
- **两小时短授权 Token 与免密直达 Magic Link (`internal/httpserver/token.go`, `internal/httpserver/server.go`, `internal/httpserver/dashboard.go`)**：构建安全可控的临时鉴权令牌管理体系（`TokenManager`，默认 2 小时 TTL）。管理员在飞书发送“管理页面”即可获取专属带参跳转 URL；访问时服务端安全校验并自动换取 2 小时有效会话 Cookie，通过 303 重定向抹除地址栏令牌敏感参数，超时自动失效并友好引导重新获取。
- **飞书机器人交互诊断子命令 (`test-bot-reply`)**：新增 CLI 命令 `./bin/twc-approval-go-bridge test-bot-reply [-text <msg>] [-open-id <id>]`，便于管理员在终端快速验证机器人私信意图识别、卡片组装与实际飞书消息投递全链路。
- **多事件共享长连接与配置拓展 (`internal/feishu/events/listener.go`, `internal/config`)**：单一 WebSocket 长连接透明合并监听多维表格数据变更与机器人消息事件；配置新增 `admin_open_ids` 管理员鉴权白名单、`public_base_url` 公网基地址与 `magic_token_ttl` 时长控制。
- **控制台顶栏选项卡与多视图路由 (`internal/httpserver/dashboard.go`)**：Web 控制台新增直观易用的顶级导航栏，支持无缝切换「📊 监控概览 (`#overview`)」、「📜 业务审计流水 (`#audit`)」与「💻 系统运行日志 (`#system-logs`)」；深度支持浏览器 URL Hash 锚点，刷新或直达链接均可精准定位对应页面。
- **实时系统运行日志控制台与接口 (`internal/httpserver/logbuffer.go`, `internal/httpserver/server.go`)**：后端实现线程安全的环形内存日志缓冲器（`LogBuffer` & `BufferedHandler`，保留最新 1,000 条 `slog` 记录），并暴露鉴权接口 `GET /api/system-logs`；前端提供极客风全黑终端界面，支持全级别过滤（ALL/DEBUG/INFO/WARN/ERROR）、关键词搜索、自动滚动、暂停与清空，并支持一键展开结构化属性。
- **飞书机器人通知通道 (`internal/feishu/notify`)**：新建通用飞书机器人通知模块，支持通过企业自建应用机器人（ebot）向指定群聊（`chat_ids`）与个人管理员（`open_ids`）广播交互式富文本卡片消息。
- **小票识别死信自动告警卡片**：小票附件连续失败 3 次触发死信止血保护时，由后台自动构建警示卡片推送到配置的群聊/管理员，卡片包含单据直达链接跳转按钮、小票文件名、Token、重试次数、错误详情与终端排查引导，并具备 1 小时内相同文件告警防抖去重机制。
- **配置支持与示例同步 (`configs/config.example.toml` & `internal/config`)**：在统一 TOML 配置文件中增加 `[notification]` 配置区块（`enabled`, `chat_ids`, `open_ids`, `notify_on_dead_letter`），实现全量严格字段解析与合法性校验。
- **通知连通性诊断命令 (`test-notify`)**：新增 CLI 诊断子命令 `./bin/twc-approval-go-bridge test-notify [-chat <id>] [-open-id <id>]`，便于管理员即时测试飞书机器人的群聊/单聊卡片投递权限。

### 变更 (Changed)
- **监控看板时间显示统一转为 UTC+8 北京时间 (`internal/httpserver/dashboard.go`, `cmd/server/main.go`)**：服务端与前端所有时间（服务启动时间、飞书长连接建立时间、最新事件接收时间、业务审计流水时间及系统终端日志时间）统一定位并格式化为 `(UTC+8)` 北京时间（Asia/Shanghai），杜绝宿主机或容器默认 UTC 时区引起的偏差。
- **飞书长连接卡片动态展示全部订阅事件 (`internal/httpserver/dashboard.go`, `internal/feishu/events/listener.go`, `cmd/server/main.go`)**：解决长连接监听事件仅静态展示单一条目的问题，支持从底层 Listener 动态获取并展示已注册的全部事件类型（如多维表格变更 `drive.file.bitable_record_changed_v1` 与机器人私信 `im.message.receive_v1`），并在前端页面中右对齐标签堆叠呈现与动态刷新。
- **飞书长连接统一共享 (`cmd/server/main.go`)**：服务启动时通过单一长连接同时监听多维表格记录变更与单聊消息接收，保持极低的网络与系统开销。
- **全局日志接入缓冲管道 (`cmd/server/main.go`)**：服务启动时通过 `BufferedHandler` 透明复制标准输出日志到内存环形缓冲区，供 Web 端随时拉取，零性能损耗且不污染外部日志管道。
- **识别流程对接死信告警 (`internal/app/recognition`)**：在小票识别流程达到最大尝试次数进入死信状态时触发 `DeadLetterNotifier`，在常驻后台（`server`）中自动联动通知服务。

### 修复 (Fixed)
- **Makefile 版本回退风险修正 (`Makefile`)**：将无 Git Tag 时的回退默认版本由 `0.3.1` 修正为与当前基线一致的 `0.3.2`，避免 `make build` 编译时通过 ldflags 意外覆写低版本号。

---

## [0.3.2] - 2026-10-11

### 架构加固、SealAI 闭环与运维能力就绪 (Architecture Hardening, SealAI & Ops Ready)

### 新增 (Added)
- **SealAI 单据原始链接直达**：向 SealAI Webhook 发起机审时注入单据原始直达链接（`DocumentRequest.DocumentURL` 与单据/明细字段 `source_record_url`，Label 为 `"单据原始链接"`），针对单行个人报销明细自动精准拼接飞书多维表格行记录展开链接（`${host}/base/${baseToken}?table=${tableID}&record=${recordID}`），支持企业专属域名配置与通用域名安全兜底。
- **飞书 Tenant Token 集中缓存 (`internal/feishu/token`)**：新增轻量、并发安全且带 TTL 内存缓存与提前刷新机制的统一 Token Provider，全系统（`base`、`card`、`task`、`provision`、`health`）收敛至单一实现，彻底消除无缓存重复向飞书换取 Token 导致的限流与网络开销风险。
- **流水过滤决策类型枚举 (`internal/feishu/card`)**：引入强类型 `DecisionCode` 枚举（如 `notify`、`already_linked`、`incomplete_fields` 等），替换原先脆弱的中文原因子串匹配统计，提高过滤规则可维护性与稳定性。
- **死信识别任务运维命令 (`cmd/server`, `internal/state`)**：新增 `list-failed-receipts` 与 `retry-receipt <id>` CLI 命令，支持运维人员查看死信附件列表、失败原因及一键重置重试计数重新发起识别。
- **SealAI 回调宽容解析与运维反馈 (`internal/seal`, `cmd/server`)**：`DecodeCallback` 增加 snake_case 别名支持与不区分大小写的审核结论解析；`apply-seal-result` 执行成功后输出标准 JSON 结果反馈。
- **状态持久化单元测试覆盖 (`internal/state`)**：补齐状态模块单元测试，覆盖识别任务的入队、加载、失败标记、死信列表与重置重试全生命周期。

### 变更 (Changed)
- **配置纯粹性治理**：彻底移除早期草稿遗留的冗余别名配置字段 `enterprise_host`，全系统统一严格收敛至单一主配置项 `[feishu] host`，杜绝二义性。
- **Seal 审核版本指纹解耦 (`cmd/server`)**：将机审状态机的 `ProviderVersion` 稳定为固定的 `"v1"`，避免包含动态 Webhook URL 导致环境配置漂移时意外触发全部在途单据指纹失效与全量重审。
- **单 Go 文件流水过滤规则整洁化 (`internal/feishu/card`)**：梳理 `filter.go` 内部两层中文列名查找逻辑，提取中心化字段别名映射表 `defaultFieldAliases` 与统一取值器 `getField`，保持单 Go 文件表达业务规则的极简性与类型安全。
- **审计记录全流程 RecordID 对齐 (`internal/app/review`)**：将机审完成与回写阶段的审计 RecordID 对齐为单据原始记录 ID (`Document.RecordID`)，确保 `audit <record-id>` 能完整按链路串联从推送、上传、OCR 到机审完成与回写的全生命周期。

### 修复 (Fixed)
- **OCR 识别失败无限重试止血 (`internal/app/recognition`, `internal/state`)**：为小票识别任务 Checkpoint 增加 `Attempts` 计数、`LastError`、`LastAttemptAt` 与 `Failed` 死信标记。当识别失败达到最大重试次数（默认 3 次）时转入死信态，终止 30 秒轮询无限扣费漏洞。
- **单据处理静默失败可观测性 (`internal/feishu/card`)**：补齐流水建单、预填明细与创建待办失败时的告警日志，杜绝静默吞掉错误导致排查无感知。
- **模型审核底层错误保留 (`internal/anthropic/review`)**：在审核错误抛出时使用 `%w` 包装底层错误，保证调用方可通过 `errors.As` 精准分类排查。


---

## [0.3.1] - 2026-10-10

### 运行时业务元数据与审计追溯体系 (Business Audit & Traceability)

#### 新增 (Added)
- **运行时业务元数据与操作审计追溯体系 (`internal/audit`)**：
  - 自动捕获全链路四大核心里程碑业务元数据：
    1. **什么时候推送了谁**：记录持卡人 open_id、姓名、流水 ID、金额、商户及消息 ID (`notify_card_sent`)；
    2. **谁填了文件**：记录明细记录中新上传的附件 Token、文件名与操作时间 (`file_uploaded`)；
    3. **什么时候发起了 OCR**：记录 OCR 任务发起时间、完成时间、提取要素（金额、币种、商户）与流水号 (`ocr_initiated` / `ocr_completed`)；
    4. **什么时候发起了审核**：记录单据送审时间、审核提供方、版本号、机审结论及回写飞书状态 (`review_submitted` / `review_completed` / `review_writeback`)；
  - 提供 CLI 快速查询命令：
    - `audit <record-id> [-json]`：打印单据全生命周期树形时间线；
    - `audit-query [options]`：支持按人员 (`-user`)、按类型 (`-type`)、按单据 (`-id`)、按时间 (`-since`) 多维过滤；
  - HTTP 端点提供 `GET /api/audit` 与 `GET /api/audit/timeline`；
  - Web 监控看板集成实时业务操作流水与单据时间线交互式查询面板。

---

## [0.3.0] - 2026-10-10

### 准上线基线版本 (Release Candidate Baseline)

本版本为系统正式上线前的基线版本，完成了架构收敛、双引擎小票识别与机审、飞书发票台账自动化双向关联、补票卡片秒级触达、版本构建追溯与变更追踪机制。

#### 新增 (Added)
- **版本机制与构建元数据追踪**：
  - 定义语义化版本 `0.3.0` 基线；
  - 编译期自动注入 `GitCommit`、`BuildDate`、`GoVersion` 与平台架构信息；
  - CLI 新增 `version`、`-v`、`--version` 支持，并支持 `-json` 输出结构化元数据；
  - HTTP 端点 `GET /version`、`GET /api/status` 与 Web 监控看板完整展示版本与构建标识。
- **变更日志机制 (CHANGELOG)**：
  - 建立标准 `CHANGELOG.md`，确立各类型变更与上线节奏的维护规范，固化“每一个修改有迹可循”协作红线。
- **飞书 WebSocket 长连接秒级同步**：
  - 引入飞书长连接监听，支持多维表格交易流水新增/更新事件秒级感知；
  - 内置防抖归集（`transactions.debounce`）与本地状态去重（`state_dir`），彻底规避全量轮询延迟。
- **双引擎海外小票 OCR 识别流水线**：
  - 集成 Anyreceipt 商业级 OCR 引擎，精准解析消费日期、商户名称、原币币种、原币金额、小票细项与税费；
  - 内置 Anthropic 多模态兼容引擎兜底识别；
  - 识别结果自动写回明细表附件校验列与原始字段。
- **飞书发票台账自动化与双向关联**：
  - 自动拆分/归集小票事实生成发票台账记录；
  - 实现个人报销明细表与发票台账的双向双向关联字段自动绑定；
  - 基于发票关键要素进行台账查重，杜绝重复报销。
- **AI 智能审批自动化流转**：
  - 支持小票识别完成后自动送审（`after_recognition`）或手动送审；
  - 对接企业级 SealAI 机审大模型，支持外部 Webhook 与本地规则引擎兜底；
  - 机审结论（通过/人工复核/不通过）、置信度与理由自动回写明细表对应列。
- **信用卡补票卡片实时推送与批量扫描**：
  - 支持单笔交易流水补票卡片实时投递（`notify-transaction`）；
  - 支持批量全量流水表扫描催报工具（`scan-transactions`），通过飞书卡片一键提醒持卡人补录。
- **运维诊断与监控看板**：
  - CLI 提供 `doctor`、`check-business-config`、`init-bitable` 工具；
  - 内置轻量 Web 监控看板（含密码认证保护），实时呈现长连接状态、Anyreceipt 剩余点数与表格拓扑。

#### 变更 (Changed)
- **单一主配置架构收敛**：
  - 彻底废弃旧 `.env` 与 `BUSINESS_CONFIG_FILE` 散落配置；
  - 运行时配置完全收敛于 `configs/config.toml`，业务表拓扑独立存放于 `configs/tables/*.json`。
- **飞书原生审批模块归档封存**：
  - 将历史飞书原生审批客户端与表单相关扩展（13,300+ 行）完整剥离并封存至分支 `archive/feishu-native-approval`（标签 `archive/native-approval-20261008`）；
  - 主线专注于海外小票 OCR 与 AI 机审。

#### 安全 (Security)
- 运行配置文件严格采用 `0600` 权限，受 `.gitignore` 保护，严禁密钥入库；
- Webhook 回调路由与监控面板具备 Token / 密码安全验证；
- 批量推送与写表工具默认开启 Dry-Run 只读保护，防止误发或污染生产表格。
