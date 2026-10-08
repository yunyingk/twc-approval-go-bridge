# 当前业务实现与交接

2026-10-08 后续配置调整：主配置现为 TOML，模板及自有规则独立；监督服务尚未切换。以下保留前阶段记录，当前结构与 Gemini 交接见[最新记录](progress/2026-10-08-config-toml.md)。

2026-10-08 更新：配置已收敛到唯一私有 `config.json`，旧环境变量和分文件示例不再生效；下文保留历史阶段说明。当前入口、迁移和验证以[单文件配置记录](progress/2026-10-08-single-config.md)为准。

更新：2026-10-05。本文记录实际实现；[架构评审](architecture-review.md)保留为实施前的评审快照，[迁移任务](architecture-tasks.md)保留原有目标和后续验收依据。

业务来源已收敛到[显式业务配置文件](business-configuration.md)：本机选择 `configs/business/enterprise-test.json`，按角色声明流水、明细、台账及提供方和触发方式。以下环境变量说明保留给未选择 `BUSINESS_CONFIG_FILE` 的兼容模式；选中文件后，对应业务变量不再覆盖文件。凭证和运行参数继续由环境提供。

Seal 真实页面与项目应用身份核验见 [Seal 测试租户实地核验](seal-live-verification.md)。同日已建立[本机公网调试入口](public-debug.md)，更新 `test` 通道并验证真实回调和重复投递。测试通道目前为辅助模式；AI 结果是建议，不代表人工批准或结算。

2026-10-04 已切换企业测试应用和新 Base，通过实际修改并恢复测试记录验证了两次 WebSocket 事件投递。表字段映射、双身份权限及 Base 订阅见[飞书事件验收](feishu-event-verification.md)；旧环境未收到事件的描述是历史记录。此后已验证真实附件事件触发 Anyreceipt、完整识别输出、台账回写及双向关联，见[企业 OCR 验收](anyreceipt-enterprise-verification.md)。项目凭证统一保存在私有 `.env`，调试覆盖文件只保存运行参数。

同日已完成企业副本的显式 Seal 送审、真实公网回调与七个专用 AI 结果字段回写；重复回调及同版本重跑复用已有结果。字段 ID、业务边界、修复原因与证据见[阶段过程记录](progress/2026-10-04-seal-review.md)。

## 已落地的业务边界

- `app/recognition` 共用附件任务、事件/轮询入队、识别结果交付和重启恢复。飞书事件 JSON 由 `feishu/base/events` 解码，台账映射在 `feishu/base/invoiceledger`。
- `core/invoice.Facts` 统一票据事实；Anyreceipt、自有模型及历史台账在适配边界归一化。`Outputs` 与旧字段仍保留，`receiptcompat` 读取历史 `outputs` / `data.outputs` 及新 `facts`。
- 金额使用十进制文本及 `json.Number`，避免经过浮点数损失精度；不把缺失值当零，不把 `1,23` 当作 `123`。
- 台账写入核对线上字段类型：数字列中的明确十进制文本转为 JSON 数字，文本列保持文本；先保留已有人工事实，再校验待补字段。真实附件事件的 `attachmentToken` 在事件适配器转换，不把附件单元格 `id` 当作下载 token。
- 送审读取台账有效列，人工修正优先于原始 OCR；原始 JSON 保留。识别再次交付保留台账已有字段，只补尚未出现在响应中的字段。此策略不提供显式重识别覆盖功能，也不能区分平台省略的空字段与尚未填写的字段。
- `app/review` 读取完整票据集合、聚合查重证据、生成版本、控制提交及应用结果；`core/review` 定义共同契约。
- `seal/review.Gateway` 只负责 Seal 上传、映射和提交。**Seal 主线不加载本地规则；规则仍在 SealAI 内维护。** `anthropic/review` 才读取本地规则，并调用独立 Messages 客户端。
- 旧 `anyreceipt/flow`、`anyreceipt/ledger` 和 Seal 编排入口保留兼容门面。当前服务直接装配新应用层，未删除原有目录。

## 运行入口

### 识别

原有 `RECEIPT_PROVIDER=anyreceipt|model` 和事件/轮询配置继续使用。`STATE_DIR` 默认 `data`，保存待处理任务、已识别结果、交付状态和首次扫描基线。重启后恢复未完成交付；基线已建立后，停机期间新增的附件会在后续轮询中处理。

识别结果在写台账前落盘。回写失败重试复用已保存结果；如果进程恰好在外部识别成功与本地落盘之间崩溃，仍可能重复识别，这不是跨系统原子事务。

同一状态目录内，通过 OS 锁限制一个来源表/字段只能运行一个识别 worker。状态实现支持本项目的 Linux/macOS 目标，适用于单主机持久卷，不支持多机器共享目录下的分布式运行。Docker 使用 `/data`，Compose 已挂持久卷。

### 版本化审核

`REVIEW_TRIGGER_MODE=manual|after_recognition` 控制送审时机，独立于 `REVIEW_PROVIDER=seal|model`。默认配置为 `manual`；本机采用用户选择的 `after_recognition`，每次成功写入台账后保存送审意图，后台等待同一明细全部附件齐备，再执行共用的版本化提交。配置切换需重启；显式命令仍可用。实现与恢复边界见[自动送审过程记录](progress/2026-10-04-automatic-review.md)。

业务文件另支持 `review.resubmit_on_detail_change` 和 `change_debounce`，可合并明细有效输入的连续修改并重新准备审核；企业测试配置目前关闭。持久化意图先于事件确认，等待时间和事件去重可跨重启，AI 回写不会触发循环。全部附件删除或来源明细删除会结束待送审，迟到结果保留为旧结果。权限失败和台账未齐继续待处理。具体范围和未覆盖的流水原表/台账联动见[配置说明](business-configuration.md#可选的明细修改重审)及[修改重审过程记录](progress/2026-10-05-review-changes.md)。

本机运行版本已更新为 `public-debug-20261005-detail-review`；公网和本地端点、长连接恢复及正式状态未变已核验。新增修改触发只进行了真实来源读取与隔离审核器验证，尚未开启企业开关做真实修改送审。

业务文件另支持独立的 `review.resubmit_on_source_change`：流水事实及台账的修改、新增、删除先进入持久化收件队列，再依据历史审核依赖和当前自动任务向受影响明细排队；包括新增同票号候选。部分排队失败可跨重启恢复，旧来源不重定向，事件不作为新的业务事实。企业配置仍关闭两个修改开关；具体字段、费用及修正冲突边界见[来源修改配置](business-configuration.md#可选的来源修改联动)和[过程记录](progress/2026-10-05-source-review-changes.md)。

来源阶段的当前本机版本为 `public-debug-20261005-source-review-legacy`。已核验真实读取、历史依赖、旧支付快照迁移范围及隔离审核器的合成事件联动，正式状态未变，公网和长连接已恢复；仍未开启企业修改开关做真实编辑付费送审。

后续审核恢复阶段已更新为 `public-debug-20261005-review-recovery`，本地、公网、长连接及诊断入口已核验。正式任务和审核结果未变化，1 个旧租户结果新增归档元数据；两份已交付企业样本与当前快照均不相同，原因包括新增支付输入及后来新增的同票号候选。没有新增付费调用或真实业务写入，见[恢复验收](progress/2026-10-05-review-recovery.md#运行更新)。

```bash
# 默认 REVIEW_PROVIDER=seal；凭证及原有 RECEIPT_* 配置由环境注入。
go run ./cmd/server submit-review <个人报销明细记录ID>

# 自有审核另需 REVIEW_MODEL_* 与 REVIEW_RULES_FILE。
REVIEW_PROVIDER=model go run ./cmd/server submit-review <个人报销明细记录ID>
```

版本取决于业务来源、有效票据事实、原件内容、查重证据、配置的业务上下文及提供方版本；临时下载 URL、抓取时间不参与版本。新命令在调用审核前保存尝试，同一版本重跑复用已保存的提交结果。

Seal 提供方版本目前标识通道 URL，不锁定其规则集。Seal 实际使用最新发布规则；当前 Webhook 不提供规则版本选择或规则更新后强制重审协议。

提交请求失去响应时保留 `unknown`，不自动再次调用供应商；明确的 Seal 4xx 拒绝（408/429 除外）记录为 `failed`。两者都需要核对原因与供应商状态，当前不提供自动查询或自动解除该尝试的接口。

审核诊断与恢复命令共用当前业务配置：

```bash
# 仅查看本地状态，不创建目录、锁文件或调用任何外部接口。
go run ./cmd/server review-status all
# 使用项目应用身份只读核对当前附件、台账、上下文和流水。
go run ./cmd/server check-review <明细记录ID>
# 只恢复已收到结果的交付，不重新送审；会核对来源与业务版本。
go run ./cmd/server retry-writeback <document-id>
```

`review-status` 的 `revision_status=not_checked` 不表示结果仍有效；`check-review` 才给出 `current/changed/source_removed/no_attachments/unavailable`。同一明细的多份历史版本分别比较。没有审核快照的指定明细会返回 `readiness`，台账未齐为 `ledger_incomplete`，同来源键冲突为 `ledger_conflict`。诊断不改变任何任务；结果只包含标识、结论和安全错误分类，不输出原票据、评论、URL 或提供方配置。

已确认来源变化的未交付结果保存为 `delivery.state=superseded`，退出每 30 秒的自动补交付。显式重试、同版本再次提交或同结果再次回调会重新核对归档版本；业务事实精确恢复后可交付原结果。已经 `delivered=true` 的显式重试是幂等空操作，其输出不替代 `check-review` 的有效性核对。核对失败及写入失败仍保留结果并自动恢复；归档保存失败不能按“旧版本已收到”确认回调。具体状态和验收见[恢复过程记录](progress/2026-10-05-review-recovery.md)。

结果接收、状态查看、版本核对和回写恢复不构造 Seal/模型审核客户端，也不加载本地规则；自有模块被编译排除后仍能完成已保存结果的交付。运行配置仍需通过统一加载校验，实际自动送审仍需所选提供方。

旧 `submit-seal` 继续固定使用 Seal，保留原 DocumentID 和未启用持久化的联调行为。正式接入版本和回调应使用 `submit-review`；不能把旧测试记录的回调当作新状态库中的已知任务。

### 业务上下文及结果回写

选择业务文件并设置 `review.include_transactions=true` 后，来源适配器会按报销明细的原生关联读取第三方交易流水，把精确金额/币种、商户、时间及资料质量问题加入共用审核快照和版本。流水保持只读；Seal 与自有审核共享证据。配置、失败处理及 `preview-review` 只读预览见[业务来源与配置](business-configuration.md)。读取事实不等于代替 Seal 维护审核规则，不推导汇率、占用或分摊。

`REVIEW_CONTEXT_FIELD_IDS` 是当前个人报销明细表中「业务语义 → 稳定字段 ID」的 JSON 映射。读取出的文本值同时供两种审核器使用，Seal 中以对应语义作为 `TEXT` 字段 key。字段 ID 在实际读取时核验，不根据历史示例推断。

查重候选包含台账有效的票号、商户、票据类型、日期、金额和币种，Seal 请求同时描述查重证据范围。按票号检索没有候选不等于全量历史查重通过；候选也不等于已经报销或结算。

```text
REVIEW_CONTEXT_FIELD_IDS={"claimed_amount":"<已核对字段ID>","claimed_currency":"<已核对字段ID>","merchant":"<已核对字段ID>"}
REVIEW_RESULT_FIELD_IDS={"decision":"<字段ID>","document_id":"<字段ID>","revision":"<字段ID>","comment":"<字段ID>"}
```

结果映射支持 `decision`、`comment`、`document_id`、`revision`、`provider`、`external_id`、`url`。启用回写至少配置前三项；必须使用独立的类型 1 文本列，不能映射到人工审批或结算状态字段。上下文与结果字段不得重叠。未配置结果字段时，结果仍保存在本地状态中。`decision` 原样保存 `approve/reject/review`，表示 AI 建议。

结果先保存，再重新核对当前业务版本，最后回写。重复结果幂等；冲突结果拒绝；旧版本结果归档而不覆盖当前明细。服务启用结果交付后每 30 秒尝试补交付，不重新调用审核器。

来源适配器在读取前核对逻辑来源标识。切换企业或 Base 后，同一状态目录中的旧来源结果继续保存，不对当前租户读取、回写或重复重试失败。`completed` 表示已收到结果，`delivered=true` 才表示飞书交付完成，两者不是同一个阶段。

飞书读取和写入之间仍有并发修改窗口；当前没有平台条件更新或业务冻结锁。版本核对不是严格的事务锁，识别完成自动送审也不提供金额冻结或人工审批锁定。

### Seal 结果接收

配置至少 32 个随机 URL 安全字符的 `SEAL_CALLBACK_TOKEN` 后，服务启用：

```text
POST /seal/callback/<SEAL_CALLBACK_TOKEN>
```

在 Seal 系统配置对应的完整 HTTPS URL。这里使用集成方提供的密钥 URL，不假设 Seal 支持任何额外签名头；它是持有 URL 即获授权的接收方式，须通过 HTTPS 及入口访问控制保护。应用日志会遮蔽路径中的 token，公网代理的访问日志也需做同样处理。

接收器限制体积并核验单据/审批记录标识及三态结论，只接受状态库中已知的 Seal 尝试。迟到结果保存后确认收到；可恢复的交付错误返回 503。企业测试副本已完成公网真实回调、专用 AI 字段回写与重复投递验收；该结果仍是辅助模式下的 AI 建议，不代表人工审批、结算或正式财务闭环已验收。

也可对已核验的回调文件执行本地导入：

```bash
go run ./cmd/server apply-seal-result /absolute/path/to/verified-callback.json
```

原本机 `/seal/callback/mock` 保留，不进行业务回写。

### 自有审核

`REVIEW_MODEL_API_KEY`、`REVIEW_MODEL_BASE_URL`、`REVIEW_MODEL_NAME` 与识别模型配置独立；`REVIEW_RULES_FILE` 只供此路径使用。示例见 [完整配置示例（现已统一）](../configs/config.example.toml)。缺少要求的上下文直接输出待复核，默认将模型批准/驳回建议转换为人工复核；只有经确认的本地规则显式允许自动决定时才保留模型决定。

自有识别支持图片、纯 JSON 和单个 Markdown JSON 围栏；拒绝被 token 上限截断的响应，发现多票标记时明确报错。PDF/多页拆分尚未实现。自有审核目前提供基于结构化事实的受控语义评审，不代表已等价覆盖 Seal 的验真、历史占用、外部数据查询与全部规则。

`no_anthropic` 构建可运行外部主线，调用自有识别或自有审核时明确返回编译排除错误。供应商真实效果与费用尚未重新评估。

## 仍需明确的业务工作

人工审批共用编排已实现 `Prepare/Submit/Reconcile`，但尚无服务命令或真实来源装配。`core/approval` 按显式轴分组，要求每行当前事实版本等于已完成 AI 审核版本及显式允许的建议；冻结计划同时保存来源/目标身份、模板、配置、发起人、成员和精确业务值。`state.Files` 按来源单文件原子保存尝试和所有成员预约；不同实例映射不得替换，未知状态不得改成释放预约的失败状态。

Feishu gateway 已接共用用例，实时模板与绑定版本变化在发送前停止；需要发起人自选的节点必须配置当前 node ID、有效 open ID 及正确单/多选数量。应用范围为 `feishu-app:<app-id>`，这是显式身份边界，不代替企业或员工归属核验。创建与查询接口已分开，恢复可用无模板/字段配置的 `InstanceLookupGateway`，并查询保存的 UUID、模板和发起人；不能借恢复重新创建。隔离 HTTP 联测见[gateway 过程记录](progress/2026-10-05-approval-gateway.md)。

后续已补 `approval` JSON 配置和 `preview-approval <记录ID[,记录ID...]>` 命令，前述“尚无服务命令”保留共用层阶段范围。预检读取已有状态及真实审核来源，不创建尝试，不调用审核方；原生目标配置完整时只读取模板元数据。它明确返回 `preview_kind=preflight`、`form_inputs_checked=false`、`creation_available=false`：完整原生字段取值、上传及计划预览尚未接入，也没有真实建单命令或自动建单。配置及企业三行只读验收见[预检记录](progress/2026-10-05-approval-preflight.md)，当前监督运行二进制仍为原版本，使用本阶段构建或 `go run` 验证。

上述为初版预检范围。现已增加 `ApprovalFieldsSource` 和 `PreparedSource`，读取精确金额、人员与关系 ID、显式时区分组，并在当前 AI 核对前后检查原生输入变化。来源绑定 version 固定进每行计划；scope 仍是稳定 Base/Table。完整 manual 配置可检查所有表单值并由实时 gateway 验证全部组，返回 `form_inputs_checked`、`form_validated` 及安全计划摘要；单行/单组失败不返回可用子集。附件要求审批上传，尚无上传/建单命令，`creation_available` 始终 false。选中企业 JSON 没有 approval，独立来源验证只读现有三行，状态 17 个文件未变；监督二进制及公网服务仍未更新。详见[来源记录](progress/2026-10-05-approval-source.md)。

来源阶段之后已补显式 `prepare-approval-files` 和 `retry-approval-files`，上传前校验整个选择的分组、当前 AI 与实时必填值，使用目标应用上传已核对的明细原件。独立持久化记录发送意图、内容/来源/目标身份及所有尝试；已确认 code 跨重启复用，只有已证明拒绝可显式重试，unknown/uploading 不重传。只读 preview 读取上传结果形成完整表单；上传后还重新核对来源与 AI。当前无建单命令/自动上传，企业文件、监督二进制和公网服务未更新，未真实上传/建单；历史“尚无上传”的段落保留阶段范围。协议限制、测试及真实只读证据见[附件记录](progress/2026-10-05-approval-files.md)。

同日新增 `prepare-approval` 完整请求审计。当前 AI/精确业务值/已存附件全部通过后，两次准备整组来源和全部 native 请求；观测到变化不保存可用子集。私有批次同时保存中立计划、实际 SDK JSON 与已核对审核证据，原件改存 SHA-256/长度，忽略读取时间及排序差异；重复准备保留原审计时间。隔离测试验证审计 body 与真正 SDK HTTP body 逐字节一致，`AuditedGateway` 可接原 Submit/UUID 恢复。当前尚无真实建单命令、批次到尝试的关联或人工结果交付，企业配置/监督二进制未变，未真实上传或创建；命令输出始终 creation_available=false。已存在的空审批 registry 或审计文件明确报错，不能按无记录处理。详见[请求准备说明](approval-configuration.md#完整请求准备)和[请求阶段记录](progress/2026-10-05-approval-requests.md)。

审计阶段之后已补显式 `submit-approval`，只接受保存的整批 ID，首次整批重核后原子预约所有组/成员。尝试不可改绑原 preparation ID/request hash，新增 reserved 区分未发送与 submitting/unknown；逐组再核对证明/表单并原子领取，领取者才发送具体已审阅 SDK body。部分失败停止后续组，已确认实例复用、未知原 UUID 查询后才恢复剩余组。`approval-status` 零网络/写入，`check-approval` 仅用原应用查询旧 UUID，不依赖当前模板/来源/模型，`abandon-approval` 仅本地释放 reserved，不撤回或释放已发送/已批准/已撤销实例。明确拒绝及本地放弃不自动重试相同 UUID，专门重试协议仍待补。当前企业配置/监督服务未变，未真实建单/通知或人工交付；先前“无建单命令”保留历史范围。详见[命令说明](approval-configuration.md#显式建单与恢复)及[提交阶段记录](progress/2026-10-05-approval-submit.md)。

后续已补 `retry-approval`，前述“专门重试协议仍待补”保留首次提交阶段范围。只显式重开有明确拒绝/放弃/同调用方未发送证明的原批次，完整重核后保留原 UUID/body/audit，原子追加轮次和成员预约检查；查询错误不覆盖原证明。旧观察的并发重试、旧轮次创建或查询更新不能写入新轮。发送意图报错且 RPC 未调用时，只有完整匹配 audit/run/私有标记能证明未发送，证明保存失败保持 submitting；unknown/60012 始终只对账。旧无 audit、原证明已丢失或另一批审计不改绑。SDK HTTP/竞态/故障测试通过，企业配置和监督服务未更新，未真实创建/通知或接人工交付；见[重试记录](progress/2026-10-05-approval-retry.md)。

新增可选 approval.observation 后，服务可接原生 1.0 approval_instance 事件并持久化查询触发，在原 UUID/应用下保存真实状态和追加历史，启动/定时轮询补查已完成实例的后续撤销。关闭创建仍可继续观察，未启用时无新增 worker；同应用 Base/审批共用 SDK 连接，独立应用使用原凭证。事件值不替代 GET 结果，reverted boolean 单独映射，查询或意图确认失败保留恢复，不释放财务占用。订阅命令只显式调用已配置模板，不在服务启动写平台；1390007 不能确认有效订阅。正式操作步骤见[监听配置](approval-configuration.md#原生审批结果监听)，验证见[阶段记录](progress/2026-10-05-approval-observation.md)。本机企业配置和监督服务没有开启，未真实订阅/审批事件验收，Base/Seal 人工结果交付仍是后续工作。

观察之后已补中立 ResultSnapshot v1，同次原 UUID 查询保留任务/原应用身份/毫秒时间、评论、流程动作、修改/撤销关联和不含临时 URL 的附件元数据。元数据变更独立版本且仅追加，重复查询不复制历史；最新失败或仅状态查询时旧流程证据不作为当前输出。命令只显示版本/数量/问题码。唯一末次人工任务才可归属决定，自动/多人同时完成/缺时间身份/未知流程均保留问题；姓名邮箱和原生表单与审计对照仍待补。企业配置/监督服务未变，没有真实结果或 Base/Seal 人工交付验收，见[结果证据记录](progress/2026-10-05-approval-results.md)。

同一计划不会重复建单，原 UUID 可在来源/模板配置改变后独立对账。明确未建单的拒绝允许新计划取得成员；未知或已创建的计划继续预约，批准/拒绝/撤销/删除也不等于财务释放。只读查看不会创建新状态文件。当前没有远端条件更新、真实来源冻结、定时选择、审批附件上传、人工结果回写或 Seal 人工结果交付；此阶段未部署运行服务、未发起真实审批，验证见[共用审批过程记录](progress/2026-10-05-approval-workflow.md)。

1. 业务冻结与撤回重提。已实现识别完成触发及可选的明细编辑重审，全部附件清空/来源删除可结束待送审并隔离迟到结果；不等于供应商单据撤销或财务锁定。停机期间未收到的业务修改事件仍需补偿机制。
2. 关联交易流水已可配置地纳入共用快照，来源变化也有独立可选的重审联动；开关尚未在企业副本启用做真实编辑送审验收。停机修改补扫、授权、汇率或历史结算事实查询仍待完善。回写前会核对当前流水版本，但多次读取和写入之间仍有并发窗口。
3. Seal 发票修正协议。官方备份明确：同一 `sourceInvoiceId` 改核心事实会 conflict，不同来源 ID 提交相同票号也会 conflict。因此保留稳定来源 ID，不能靠更换 ID 绕过冲突；新单据版本并不提供 Seal 发票事实原地更新能力。
4. 飞书分组审批实例、跨租户人员映射、人工结果及 Seal `manual-result` 同步；已补实例创建/UUID 查询及实时模板明细映射适配，真实模板读取与本地映射通过，服务业务尚未接入。旧模板对当前桥接应用不可用，不能自动回退旧企业建单；见[接口阶段记录](progress/2026-10-05-native-approval-client.md)。
5. 历史占用、合法分摊、结算、记录可见性与锁定；真实字段和平台权限核对后实现。
6. 正式环境结果字段、持久卷与 HTTPS 入口配置，并进行项目应用身份下的真实验收。

后续实现按这些业务前置条件继续；不再以供应商目录容纳共用流程，也不要求 Seal 主线维护本地规则文件。
