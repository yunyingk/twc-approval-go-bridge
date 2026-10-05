# 项目协作规则

## 范围与工作目录

- 本文件适用于 `twc-approval-go-bridge/` 仓库；它是独立 Git 仓库，业务资料在上级目录的 `doc/`。在本仓库开发前先读 `README.md` 和相关需求文档。
- 项目级规则统一放在 `AGENTS.md`。需要项目技能时使用 `.agents/skills`，不要创建 `.codex/skills`。
- 结对编程，回答精炼；重大架构或业务改动先对齐方案，轻量修复可直接实施。遵循最小 Diff，保留已有注释和文档；破坏性文件或 Git 操作前取得显式确认。
- 完成一个经验证的阶段后及时创建本地 Git commit，避免后续改动混在未提交的工作区中。
- 在 `docs/progress/` 保留关键开发发现、实施选择与阶段验收的 Markdown 过程记录，随对应阶段提交；完整私有载荷和含凭证的运行日志不进入 Git。

## 飞书身份与配置

- 本服务使用飞书**应用身份**（`tenant_access_token`），凭 `FEISHU_APP_ID` 和 `FEISHU_APP_SECRET` 访问获授权的资源；不要为本项目发起用户 OAuth、要求用户扫码，或把用户身份的 `lark-cli --as user` 当作服务权限验证。
- 读取线上多维表格时，以项目应用身份和该应用的资源授权为准。先只读验证 Base、Table、字段及少量记录；权限不足时核对应用权限和资源授权，不擅自切换为用户身份。
- 多维表格记录变更事件需同时核对后台的应用身份与用户身份 `bitable:app` 权限，以及目标 Base 的云文档订阅；开通用户身份权限不等于运行时改用用户 OAuth。具体要求和企业测试验收见 [飞书事件联调](docs/feishu-event-verification.md)。
- 本地 `.env` 含敏感配置，不提交、不输出密钥；当前程序从进程环境变量读取配置，不能假定它会自动加载 `.env`。
- 项目凭证统一保存在私有 `.env`（0600）；`.env.public-debug` 只保存调试参数，不重复声明凭证，也不以空值覆盖 `.env` 的密钥。运行包装器按示例、`.env`、调试参数的顺序加载。
- 上级 `doc/` 已记录测试用 Base ID、Table ID、字段和审批模板。这些是已有线索；实现前仍要核对线上结构，不能把历史方案中的示例当作当前接口契约。

## 实现边界与验证

- 现有入口只运行基础 HTTP 服务和飞书事件长连接。`internal/feishu`、`internal/seal`、`internal/core/dedupe` 等目录中的部分内容只是接口边界，不等于端到端业务已实现。以 `README.md` 和代码现状为准。
- 不在接口、鉴权、字段与回调格式未经确认时硬编码业务假设。Anthropic 是独立能力，不归入 Seal 或票据识别域。
- Go 版本以 `go.mod` 为准。代码改动后运行与改动相关的测试；常规检查使用 `go test ./...` 和 `go vet ./...`。

## 当前架构实现（2026-10-03）

- 共用业务编排位于 `internal/app`，标准票据和审核契约位于 `internal/core`；外部 SDK、事件 JSON 和供应商协议保留在相应适配目录。
- SealAI 规则由 SealAI 系统维护；只有自有 Anthropic 审核使用本地规则文件。识别和审核分别选择提供方。
- 当前已实现持久化识别任务、版本化送审、可配置的 Seal 结果接收及 AI 结果回写；测试租户公网真实回调已验证，正式结果字段和完整财务流程仍需联调。旧入口描述是历史阶段范围，当前状态以 [运行与交接说明](docs/runtime-and-handoff.md)为准；本机连接方式见[公网调试入口](docs/public-debug.md)。
- 改动提供方装配或编译边界时，还需运行 `go test -tags no_anthropic ./...` 和对应构建检查。
- 2026-10-04 已切换企业测试应用及新 Base，实际修改并恢复测试记录后收到两次 WebSocket 记录变更事件；配置示例中的 Base、Table 和字段映射已按新副本核对。
- Anyreceipt 已于 2026-09-29 通过真实识别及台账回写；密钥于 2026-10-04 从历史会话恢复并统一放入私有 `.env`。新企业副本已验证真实附件事件触发、21 个识别输出、台账回写及双向关联，见 [企业 OCR 验收](docs/anyreceipt-enterprise-verification.md)。运行配置缺少凭证时先核对 `.env` 与交接记录，不将其误判为从未联调。
- 同日企业副本已通过显式 Seal 送审、真实公网回调及七个 AI 专用文本列回写验收。旧来源任务先隔离再读取，结果列按线上类型校验；人工审批、锁定及结算不由 AI 回调驱动。字段与过程证据见 [审核回写过程记录](docs/progress/2026-10-04-seal-review.md)。
- 用户选择当前启用 `REVIEW_TRIGGER_MODE=after_recognition`，保留 `manual` 配置切换。全自动附件识别、台账、Seal 送审及结果回写已通过企业副本真实验收；意图持久化并按代次确认，同一版本复用原审核尝试。详见 [自动送审过程记录](docs/progress/2026-10-04-automatic-review.md)。
- 业务来源统一使用 `BUSINESS_CONFIG_FILE` 选择 JSON，本机为 `configs/business/enterprise-test.json`；文件完整决定流水/明细/台账、字段、提供方和触发方式，对应旧业务环境变量不覆盖文件，凭证仍由环境提供。明细与台账目前必须同 Base；一个进程只运行一份配置。换副本前用应用身份执行 `check-business-config`，核验字段类型和关联目标，不能把来源声明当作跨表交易或 Webhook 流程已实现。见 [业务配置说明](docs/business-configuration.md)。
- 2026-10-05 已启用 `review.include_transactions=true`，通过原生关联真实记录 ID 只读获取流水，标准支付证据供两个审核方使用并纳入版本。流水与明细须同 Base；资料缺失显式标注，API/配置错误阻止准备；不猜汇率、发票占用或分摊。`preview-review` 不创建任务或调用付费提供方。流水变化本身尚不自动送审，多次读取与回写仍有并发窗口；此次真实验收为流水读取及已有票据快照预览，未新增付费送审。见 [交易流水过程记录](docs/progress/2026-10-05-transaction-review.md)。
- 同日已补独立可选的明细修改与来源修改重审，两个开关在企业配置中均关闭，保留识别完成触发。来源事件先持久化，再按历史审核依赖及当前自动任务定位受影响明细，覆盖新增查重候选和部分排队恢复；不改第三方流水、不绕过 Seal 发票核心事实冲突。见 [来源联动记录](docs/progress/2026-10-05-source-review-changes.md)。
- 同日新增 `review-status`（只读本地）、`check-review`（应用身份核对当前事实）、`retry-writeback`（只恢复已存结果）。诊断与交付不构造审核提供方；安全错误分类与交付阶段独立保存。归档旧结果退出自动补交付，显式操作仍可重新核对；`unknown/failed` 不自动重发。见 [审核恢复记录](docs/progress/2026-10-05-review-recovery.md)。
- 同日新增原生审批实例客户端及实时 `fieldList` 映射，官方 UUID 查询/冲突协议已归档。旧模板对当前桥接应用返回 `1390002`，旧审批应用仍可读取，两个应用缺少企业身份查询权限；不假定同租户，不自动回退，不复用跨应用 open ID。当前只做读取/本地映射，实例业务未启用，未发起真实审批。见 [实例接口记录](docs/progress/2026-10-05-native-approval-client.md)。
- 同日新增 `core/approval` 与 `app/approval` 共用人工审批用例，计划保存精确值和当前 AI 版本，单来源状态库原子预约全部成员。重复、未知及保存失败通过原 UUID 恢复；已确认实例不可改绑，终态不回退 pending，财务释放尚未实现。配置/真实来源/人工交付仍未装配；本阶段仅隔离测试，见 [共用审批记录](docs/progress/2026-10-05-approval-workflow.md)。
- 同日新增 Feishu `InstanceGateway`，用实时模板及本地绑定/自选审批人生成配置版本，准备及创建前核对必填表单、业务值类型和当前流程节点；`InstanceLookupGateway` 仅需原应用客户端和已存计划，不能建单。`60012`/内部错误/断连保留未知，仅已确认拒绝释放建单预约；隔离 HTTP 与共用状态联测通过，真实业务仍未启用，见 [gateway 记录](docs/progress/2026-10-05-approval-gateway.md)。
- 同日新增可选 `approval` 配置及 `preview-approval` 预检，明确目标凭证组、分组/时区、AI 策略、来源输入与模板绑定；无默认身份回退。`app/approval.ReviewGate` 只读校验原审核 pin 下当前事实，多份当前版本不按排序选一个。真实三个样本返回两份 stale、一份 no_review，17 个状态文件未变；表单取值、上传及真实建单仍未启用，运行二进制未更新，见 [预检记录](docs/progress/2026-10-05-approval-preflight.md)。
- 同日后续补 `ApprovalFieldsSource` 与 `PreparedSource`，按字段 ID 解析精确值、关联 record ID、时区及显式人员映射，来源绑定版本参与计划 UUID；准备期间观测到值变化即阻塞，不能将多次读取当作事务冻结。完整配置可校验全部分组的真实模板表单并返回计划摘要；附件仍要求审批上传。独立企业来源只读验证及状态文件不变已核对，配置/运行服务未更新，没有真实建单，见 [来源记录](docs/progress/2026-10-05-approval-source.md)。
- 同日补审批附件上传、独立发送意图及历次状态。`prepare-approval-files` 在全部组选中行的当前 AI/来源/必填值通过后上传已核对明细原件，preview 只读复用已存 code；`retry-approval-files` 只重试明确拒绝，unknown/uploading 保持停止。计划固定内容/来源/目标上传身份，不猜 URL 期限等于 code TTL，不跨应用复用。仍无建单命令，企业配置和监督服务未更新，未真实上传/建单；见 [附件准备记录](docs/progress/2026-10-05-approval-files.md)。
- 同日新增 `prepare-approval`，两次核对整个选择及全部 native 请求后，原子保存完整私有审计批次：计划/来源/目标/精确值、实际 SDK body、AI 事实/结果/pin，原件只存 SHA-256/长度。SDK 实际发送与审计一致已有隔离 HTTP 验证，`AuditedGateway` 可接共用 Submit/UUID 恢复；尚无真实建单命令、批次到尝试关联或人工交付。现有空审批 registry/审计文件报错，不作为无记录放行；企业配置和监督服务未变。完整载荷仅进 0600 私有状态，不提交 Git；见 [请求准备记录](docs/progress/2026-10-05-approval-requests.md)。
- 同日后续已补 `submit-approval/approval-status/check-approval/abandon-approval`，参数为已存 preparation ID。首次整批重核后原子预约所有组，固定 audit 关联；reserved 才能领取一次发送，部分失败保留未知与未发送组。恢复只按原应用/UUID，不依赖新模板/来源/模型；本地放弃仅释放 reserved，不能忘记已发送实例或财务占用。旧无 audit 尝试不可改绑，同 UUID 明确失败不自动重试。隔离 SDK HTTP/并发/保存失败已测，企业配置和监督服务未变，未真实建单或人工交付；见 [建单记录](docs/progress/2026-10-05-approval-submit.md)。
- 同日后续补 `retry-approval` 显式重试原批次。完整重核后只重开有原拒绝/放弃/同调用方未发送证明的失败，Plan/Audit/UUID/body 不变；原始证明与历次轮次独立于最新查询诊断。原子比较观察的 run 并重查成员预约，旧轮次结果不能覆盖新轮；保存意图报错时，只有未调用 RPC 且 audit/run/标记完全匹配的原调用方能证明未发送，证明保存失败继续 submitting。unknown/60012 不重发、另一批审计不改绑，旧无证据不补造。隔离 SDK/并发/故障已测，企业配置/监督服务未变，未真实建单/人工交付；见 [重试记录](docs/progress/2026-10-05-approval-retry.md)。
- 同日新增独立可选 `approval.observation`，关闭创建仍能按原应用/UUID 观察历史实例。1.0 approval_instance 通知先持久化再真实查询，重复意图幂等，查询/保存失败跨重启恢复，定时补查 finished 后撤销；事件值不作权威结果，不释放财务占用。共用传输移至 internal/feishu/events，Base 筛选仍在原目录；同应用一条连接，独立应用另开。subscribe-approval-events 仅显式订阅配置模板，1390007 不视为确认有效。企业配置/监督服务未启用，未真实订阅/审批事件验收，Base/Seal 人工交付仍待实现；见 [监听记录](docs/progress/2026-10-05-approval-observation.md)。
- 同日补 ResultSnapshot v1：原 UUID GET 同次保存中立任务/原应用 open ID/毫秒时间、评论/时间线与不含临时 URL 的附件元数据；内容版本和历史只追加，重复查询仅更新新鲜度。晚到查询不覆盖新证据，最新失败或仅状态 gateway 不将旧流程当当前，审计包装保留 rich/status_only 边界。唯一末次人工任务才提取决定，自动/同时多候选/缺身份时间/未知流程保留问题；普通评论不冒充驳回理由。企业配置/监督服务未启用，姓名邮箱解析、原生表单对照、Base/Seal 人工交付仍待实现；见 [结果记录](docs/progress/2026-10-05-approval-results.md)。
