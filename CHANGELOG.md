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
- **SealAI 单据原始链接直达**：向 SealAI Webhook 发起机审时注入单据原始直达链接（`DocumentRequest.DocumentURL` 与单据/明细字段 `source_record_url`，Label 为 `"单据原始链接"`），针对单行个人报销明细自动精准拼接飞书多维表格行记录展开链接（`${host}/base/${baseToken}?table=${tableID}&record=${recordID}`），支持企业专属域名配置与通用域名安全兜底。
- **飞书 Tenant Token 集中缓存 (`internal/feishu/token`)**：新增轻量、并发安全且带 TTL 内存缓存与提前刷新机制的统一 Token Provider，全系统（`base`、`card`、`task`、`provision`、`health`）收敛至单一实现，彻底消除无缓存重复向飞书换取 Token 导致的限流与网络开销风险。
- **流水过滤决策类型枚举 (`internal/feishu/card`)**：引入强类型 `DecisionCode` 枚举（如 `notify`、`already_linked`、`incomplete_fields` 等），替换原先脆弱的中文原因子串匹配统计，提高过滤规则可维护性与稳定性。

### 变更 (Changed)
- **配置纯粹性治理**：彻底移除早期草稿遗留的冗余别名配置字段 `enterprise_host`，全系统统一严格收敛至单一主配置项 `[feishu] host`，杜绝二义性。
- **Seal 审核版本指纹解耦 (`cmd/server`)**：将机审状态机的 `ProviderVersion` 稳定为固定的 `"v1"`，避免包含动态 Webhook URL 导致环境配置漂移时意外触发全部在途单据指纹失效与全量重审。

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
