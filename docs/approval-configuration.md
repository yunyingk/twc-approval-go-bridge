# 人工审批配置与只读预检

2026-10-05，业务来源仍由选中的 `BUSINESS_CONFIG_FILE` 决定。新增可选 `approval` 对象；当前企业配置没有启用它，既有 Anyreceipt/Seal 与自动送审设置不变。

```json
"approval": { "mode": "disabled" }
```

[关闭示例](../configs/approval/disabled.fragment.example.json)是配置片段，不是完整业务文件。`disabled` 允许保留不完整草稿；未知 JSON 属性仍拒绝。`manual` 表示显式人工选择的配置口径，**本阶段没有建单命令或自动建单装配**，不会仅因改此值创建审批。

上述“没有建单命令”保留初版预检阶段范围。后续已新增显式 `submit-approval` 与状态/恢复命令，见[建单与恢复](#显式建单与恢复)。仍无自动建单，仅配置 manual 不触发实例创建；当前企业配置未启用 approval。

## 显式契约

| 字段 | 口径 |
| --- | --- |
| `target_identity` | 必须选 `bridge` 或 `approval`；分别使用 `FEISHU_APP_ID/SECRET` 或 `FEISHU_APPROVAL_APP_ID/SECRET`，缺少所选组不回退 |
| `template_code`、`submitter_open_id`、`department_id` | 明确目标应用下的模板、发起人和可选部门；不读取旧 `FEISHU_APPROVAL_CODE` 作为默认值 |
| `allowed_ai_decisions` | 明确列出允许进入人工流程的 `approve/reject/review`；它是桥接的人工路由策略，Seal 规则继续在 Seal 系统维护 |
| `group_by` | 必须显式给数组；每项包含唯一 `axis`、表角色 `role` 及角色字段语义 `field`。空数组表示将明确选中的记录作为一批 |
| 分组的 `period`、`timezone` | `period=day/month` 要求有效时区及类型 5 日期；省略 period 按精确值。关联使用真实 record ID 和目标 table；多关系不取第一项 |
| `input_fields` | 原生业务语义到 `{role, field, kind}`；物理角色支持 `reimbursement_details/transactions`，field 引用现有角色字段映射，kind 为 text/date/number/money/people/files |
| `currency`、`currency_field` | 仅 money 使用，必须恰好提供一个：明确三字母币种或同一角色中已声明的币种字段；不猜默认币种 |
| `person_map` | people 可显式列出来源应用 open ID 到目标应用 open ID；选独立 approval 身份时必须提供，不按员工姓名匹配 |
| `role=review` 输入 | 仅允许当前已存 decision/comment 作为 text；不从 Base 的 AI 显示列取结果 |
| `detail_control`、`form_fields` | 分别指向明细控件及其语义控件；selector 恰好使用系统 id 或 custom_id，同一语义必须有输入和模板绑定 |
| `node_approvers` | 目标应用的节点 ID 到唯一 open ID 列表；在线 gateway 还检查当前节点是否自选及单/多选数量 |

交易流水输入或分组必须启用 `review.include_transactions`，且流水与明细同 Base；不在人工审批中临时接入未经审核的另一套支付来源。当前不支持按多张台账推导白名单、报销占用、发票分摊或结算事实；缺失数据需要实际字段/服务来源，不能填猜测值。

来源适配现已按稳定字段 ID 核对实时类型，直接解析原始 JSON。文本支持文字/单选/自动编号，数字和金额支持文字/数字列，日期为类型 5，人员为类型 11，附件为类型 17；公式与查找引用不做隐式转换。精确十进制不经 float64；币种字段只能为文字/单选。可选文本可空，必填由实时模板判断。关系和人员分组要求单值；流水输入要求明确单笔关联，缺失/多笔不猜分摊或求和。

人员映射校验格式及完整性，不代替目标企业的人员/发起权限核验。附件列解析真实 Base 引用，再匹配当前审核核对过的原件与已存上传结果；未准备时报告 approval_upload_required，不能将 Base token 当审批 file code。显式上传及恢复见下方，配置存在不代表完整表单可用。时区数据编入程序以支持静态镜像，未配置日期规则时不默认月份或时区。

## 预检命令与结果

```bash
# 需注入当前项目应用凭证、业务文件及已有 STATE_DIR。
go run ./cmd/server preview-approval <记录ID[,记录ID...]>
```

多个记录显式以逗号分隔，不能传 all；空项、重复或带空白的 ID 被拒绝。不构造 OCR/审核方，也不读取本地模型规则。命令从已存请求保留提供方/规则 pin，只读获取当前票据、原件内容、台账、查重候选、上下文及配置的交易流水。来源 scope 为稳定 Base/Table，不加入分组、字段或提供方配置摘要。

`records[].code` 包括 no_review、review_stale、review_pending/unknown/failed、review_conflict、review_state_invalid、policy_not_configured、decision_not_allowed，以及 source_removed/no_attachments/ledger_incomplete/ledger_conflict/source_unavailable。同一 pin 只准备一次，但分别对比每份历史 revision；多份仍当前的 pin 不按排序或旧 delivered 标记挑选。无显式策略时没有可用审批证据。

`current_facts=true` 只说明审核事实与该唯一快照匹配，在途状态仍不能进入审批。`code=current` 表示当前 AI 门禁通过；完整表单未准备时仍不能创建。诊断 JSON 不包含原件、完整事实、OCR 原文、审核评论、链接或提供方 pin。

配置完整且 mode 为 manual 时，预检使用所选目标应用只读获取 ACTIVE 模板及节点元数据；目标不可读不回退旧应用。`source_inputs[].issues` 逐行给出字段/关系/币种/人员/上传阻塞，`form_inputs_checked=true` 仅表示取值检查已执行。所有选择均通过当前 AI 门禁和来源检查时，生成完整中立计划，再由 gateway 核验实时必填控件与类型；全部组通过才返回 `form_validated=true` 和 `plans` 摘要（UUID/revision、成员 ID、轴）。任一组失败不返回部分计划，业务值和评论不进入诊断输出。

表单读取前后夹一次当前审核核对，观测到准备期间变化报告 source_changed；它不是远端事务锁，也不表示新配置的所有表单字段都曾进入原 AI 审核。来源绑定版本参与计划 UUID，Base/Table scope 保持稳定。模板/表单验证不代替真实人员权限和财务流程验收。

输出始终为 `preview_kind=preflight`、`creation_available=false`；来源未检查时保留 form_source_preparation_pending。本命令不领取成员、不写状态或 Base、不上传、不通知、不建单，也不更新运行服务。企业选中配置仍没有 approval，因此当前三条真实记录只返回 AI 预检，不生成计划。

使用本阶段构建或 `go run`；本机运行包装器仍使用先前监督二进制，需后续受控更新后才具备此命令。AI 三行预检见[预检记录](progress/2026-10-05-approval-preflight.md)，独立来源读取及计划校验见[来源记录](progress/2026-10-05-approval-source.md)。

## 审批附件准备

```bash
# 使用完整 manual 配置和原有 STATE_DIR；不会创建审批实例。
go run ./cmd/server prepare-approval-files <记录ID[,记录ID...]>
# 只对已经证明拒绝的上传追加一次新尝试；未知响应仍停止。
go run ./cmd/server retry-approval-files <记录ID[,记录ID...]>
```

此命令会向所选审批应用上传原件并保存本地状态。先检查整组选中的当前 AI 结果、来源稳定性、分组和各组实时必填表单；全部通过才逐文件上传。当前只接受审核快照中已经核对的明细附件，不临时下载另一份原件、不上传未进入审核的流水附件。附件元数据与审核原件不一致时停止；来源绑定与人员身份仍使用现有显式配置。

每份上传保存发送前意图，身份涵盖内容 SHA-256、来源 Base/Table、应用、行、字段、文件 token、文件名/MIME/长度、目标应用及用途。同一份已确认结果在重启和并发调用中复用；来源或目标变化得到另一身份。历史上传记录不能作为跨记录、跨应用通用文件缓存。核心计划保存上传身份 provenance，即使供应商返回相同 code，另一份原件也改变计划版本。

| 本地状态 | 下一步 |
| --- | --- |
| 无记录（approval_upload_required） | 显式 prepare 命令保存意图并上传 |
| uploaded | 复用确认的目标应用 file code |
| rejected（approval_upload_rejected） | 普通 prepare 停止；显式 retry 保留旧记录并追加尝试 |
| uploading/unknown（approval_upload_reconcile） | 禁止重发，包括 retry 命令；需外部核查 |
| 状态无法读取/格式不符 | 停止，不能当作未上传 |

只有文件 POST 尚未发生的本地/鉴权失败，或一致 HTTP 响应中的官方鉴权、权限/IP 拒绝，才记为 rejected；断连、缺字段、5xx、429 等保留 unknown。上传已经成功但本地结果保存失败时，原意图保留 uploading，后续停止。官方没有上传幂等键或结果查询协议，当前不编造自动对账或清除未知状态。12 小时是下载 URL 有效期，不是 file code TTL；不保存 URL，也不按时间猜测失效并重传。已存 code 在完整实例表单中仍需按实际接口验证。

一份成功、后一份失败时保留成功上传，不回滚也不在下次重传。上传后重新检查所有来源和当前 AI，再完整校验全部表单；有变化就不输出可用计划。输出只含安全状态/问题/计划摘要，不含原件、文件名、code、来源 token 或完整表单。`file_preparation_complete` 和 `form_validated` 必须分别检查：语义阻塞也返回诊断 JSON，不应只用进程退出码判定业务成功；`creation_available` 始终 false。

状态元数据包含敏感来源标识和文件名，保存到 0600 文件及独立 native_approval_upload_version 命名空间，不能公开或提交 Git；原件二进制不入状态。预览缺少凭据时只读返回阻塞，不创建状态或锁文件。本阶段未启用企业 approval 配置、未更新监督服务、未真实上传或建单。验证与下一步见[附件准备记录](progress/2026-10-05-approval-files.md)。

## 完整请求准备

```bash
# 使用完整 manual 配置；先按上面的命令准备所需审批附件。
go run ./cmd/server prepare-approval <记录ID[,记录ID...]>
```

这个命令只读取线上来源和模板，并保存本地私有审计文件。整组选中行必须有唯一、已完成且符合显式策略的当前 AI 结果，以及完整业务值和所需审批 file code。它不会补上传、调用审核方、预约成员或创建实例；企业选中配置尚未启用 approval，会在状态/网络操作前拒绝。

准备时生成全部分组的中立计划和实际 SDK 建单请求体，随后重新核对整组来源、AI 与每个请求；任何一行或一组失败、两次准备之间观测到变化，整批都不保存，不返回可用子集。已保存的批次含：

- 所有计划的 UUID、来源/目标/配置版本、发起人、分组、成员、精确业务值及审批附件引用/上传身份。
- 当次核对的完整票据事实、OCR 原始响应、查重候选、配置的支付证据、审核提供方/规则 pin 及实际 AI 结果。原件二进制和附件临时下载 URL 不保存；每份原件另存内容 SHA-256 与实际字节数。快照 revision 仍引用原始完整 AI 请求，不能把去掉二进制的审计快照再次当作审核请求。
- Feishu 实际建单 JSON（`request.format=feishu.instance-create.v4`），包含字符串化 form、目标应用下人员/file code、原 UUID、自选节点审批人及两个关闭的重提开关；不含鉴权头或应用密钥。

保存到 `STATE_DIR` 的独立 `native_approval_preparation_version=1` 文件，文件权限 0600。stdout 只提供 `preparation_id`、`private_audit_file`、计划/成员/版本及请求 SHA-256，不打印私有值。可在本机打开报告给出的私有文件 review；它与 `.env`、完整运行日志一样不能提交 Git 或公开。原子保存只保证本地整批快照，不冻结远端 Base。

计划与审核证明的 JSON 载荷上限为 8MiB，超过时整批停止。审计采用单份 JSON 文件；本机格式化查看可读性时，也需保留其私有权限。

`preparation_complete=true` 说明整个批次已成功保存；必须检查这个字段和 `issues`，不能只看进程退出码。`creation_available` 始终 false。整批摘要不含保存时间；读取时间、返回顺序和附件临时 URL 变化不产生新审计，同一批次重复准备保留最初保存时间。内容、AI 结果、人员、模板或请求变化则产生不同摘要；摘要用于完整性核对，不是权限凭证。

`issues` 包括 source_not_ready、plan_inputs_invalid、target_request_invalid、review_proof_missing、review_proof_invalid、preparation_invalid、preparation_changed；具体来源原因仍在 records/source_inputs。状态读取/保存或上下文取消失败会返回非零退出码，不输出成功报告；已有空/损坏快照不会当作缺失记录覆盖。

底层 `AuditedGateway` 已在隔离测试中将已保存的具体请求接到共用 Submit，用同一原生 body 构建器逐字节核对后发送，仍执行来源重核、成员预约及原 UUID 恢复。本阶段没有真实建单命令，尚未将批次 ID 关联进实际建单尝试或接人工结果交付；完整请求准备不能代替这部分验收。验证及下一步见[请求审计记录](progress/2026-10-05-approval-requests.md)。

## 显式建单与恢复

```bash
# 先 review prepare-approval 给出的私有文件；参数是整批 SHA-256，不是记录 ID。
go run ./cmd/server submit-approval <preparation_id>
# 只读本地审计/实例映射，零网络、零状态写入，不需应用凭证。
go run ./cmd/server approval-status <preparation_id>
# 只按已存 UUID 查询原应用实例，并保存核对结果；不重建、不重新审核。
go run ./cmd/server check-approval <preparation_id>
# 本地放弃尚未发送的 reserved 组；不会撤销或释放已发送的实例。
go run ./cmd/server abandon-approval <preparation_id>
```

仅接受一个已保存的、小写 64 位 SHA-256 批次 ID，不接受 all、记录列表、UUID 或外部 JSON 文件。建单入口需要当前选中完整 manual 配置及显式目标凭证，物理来源与目标应用须等于审计；缺配置在状态/网络装配前失败。来源/分组/人员/AI 策略/绑定/模板/精确值或实际请求变化，都不能把旧审计重新解释为新请求；需重新准备并 review。

首次提交先重新核对整批来源、当前 AI 证明、所有原生请求，然后在来源 registry 的一个原子替换中预约全部新组和成员、保存固定 audit `{preparation_id,request_format,request_sha256}`。后一组成员已被另一计划占用时，前面的空闲组也不预约、不发送。已存相同计划但来自另一批审计，或早期没有 audit 的尝试，不能静默改绑。审批 UUID 保持计划原值。

逐组发送前再核对该组完整审核证明/业务值和实时表单，并原子领取发送意图，只有领取者发送一次。每个 HTTP body 必须与具体审计一致。任何组失败停止后续发送，保存已确认实例及后面的未发送预约；外部 API 没有整批事务，因此不能保证多个实例全部创建或全部不创建。

| phase | 含义及下一步 |
| --- | --- |
| not_started | 没有本地尝试；已审阅批次可显式提交 |
| reserved | 全组已预约，此组尚未领取发送；重核后可继续，或显式放弃 |
| submitting/unknown | 已持久化发送意图，可能已创建；先 check-approval，禁止重发或本地放弃 |
| pending/finished | 已确认原实例映射；重复提交仅返回原映射，不重核或重新创建该实例 |
| failed | 已证明拒绝或本地放弃未发送预约；同一 UUID 不自动重发 |

同一批中有 submitting/unknown/failed 时，提交入口先停止，返回 reconciliation_required。响应丢失但原 UUID 查询成功后，重新提交只处理仍 reserved 的组。查询失败、权限不足或未找到，保持原预约与不确定性；不能换 UUID 绕过。若领取意图的原子保存已发生但调用方收到错误，即使这次实际未 POST，也保留 submitting，下一次不能据此重发。专门的明确拒绝重试及未发送证明修复协议仍待实现；单纯修复权限后，若仍为同一 UUID，当前也不会重发。

`approval-status` 只读本地。`check-approval` 使用审计/已存计划的原目标 `feishu-app:<app-id>`，只允许现有 bridge/approval 凭证组中恰好一组匹配这个 ID；缺原密钥、匹配不到或两组重复声明同 ID 都停止，不回退新应用。无需当前审批模板、表单映射、Base 来源、OCR/审核提供方或本地规则。reserved、未开始、本地 abandoned 不查询；真实查询仍按原 UUID/模板/发起人核对，保存真实实例和安全失败分类。

`abandon-approval` 仅在本地原子地将此批仍 reserved 的组记为 failed，failure=`preparation/abandoned`，保留原计划/审计及记录。它与领取意图使用同一 registry 锁；一旦其他进程先领取，就不能放弃。submitting/unknown/pending/finished 不改动、不会解除其成员占用，也不调用远端撤回。放弃后的相同 UUID 不能直接再次提交；有新的业务/配置版本时才能另行准备计划。此操作不提供人工撤回、财务释放或结算能力。

所有命令只打印安全状态/成员/UUID/实例 Code/审计摘要/错误分类。`status.all_instances_known` 只表示全部组已有实例映射，不能当作当前事实、人工批准或结算完成；`instance_verified` 区分创建响应与实际查询。提交/查询失败会先输出可读取的部分状态（若状态仍可读），然后非零退出，不打印上游私有错误。原件、表单、评论和凭证仍在私有文件中。

当前企业未配置原生审批，也没有可用的完整当前 AI 样本。本阶段仅隔离建单/恢复联测及真实只读验证，没有真实上传、实例创建或通知，监督服务未更新。人工事件、Base/Seal 结果交付、正式财务动作与真实验收继续后续实施，见[建单过程记录](progress/2026-10-05-approval-submit.md)。

## 显式重试原请求

上述“专门重试协议仍待实现”保留首次建单阶段范围。后续已补：

```bash
# 修复明确拒绝的原因后，使用原来已审阅的批次；保持原 UUID 和请求。
go run ./cmd/server retry-approval <preparation_id>
```

要求完整 manual 配置及原来源/目标匹配，并再次核对整批当前 AI、业务值、上传引用和原生请求。普通 submit 仍不自动重试 failed。显式 retry 只重开带原始拒绝/本地放弃/同调用方未发送证明的 failed；submitting、unknown、未找到和 UUID 冲突保持原 UUID 对账。完全未提交的批次返回 not_submitted，已确认组只复用原映射，原 reserved 组可继续。若成员已被别的计划取得，整批停止。

状态新增 run（初次 0）、closed_runs、no_creation、retryable。重开前原子归档该轮原始证明和最后诊断，保持 Plan/Audit/UUID 不变；另一批审计仍不可改绑。查询失败不覆盖拒绝证据，查询确认实例后则不再重发。旧状态只有完整、带有效时间的原失败证据才能重试，generic failed/lookup_failed 不补造证明。retryable 是本地恢复资格，仍需通过实时重核和预约。

领取发送意图报错、但同调用方确实尚未调用 RPC 时，程序可凭完全匹配的 audit/run/私有发送标记保存 persistence/not_sent。证明保存失败继续保持 submitting，重启不能自行认定未发送；远端请求已经调用后绝不使用此分支。本地状态及 stdout 不输出发送标记或私有表单。该证明和本地 abandoned 不需要远端查询；明确拒绝仍可 check-approval。

隔离 SDK、并发、旧轮次迟到和保存失败验证见[重试过程记录](progress/2026-10-05-approval-retry.md)。原请求、来源或模板发生实际变化时本命令停止；部分成功后的批次拆分/审计重关联仍待独立实现。当前企业未启用 approval，监督服务未更新，没有真实建单或人工结果交付。

## 原生审批结果监听

可选的 `approval.observation` 与创建开关独立。监听已建实例可以关闭创建，不需要当前表单、人员映射或 AI 配置：

```json
"approval": {
  "mode": "disabled",
  "target_identity": "bridge",
  "observation": { "enabled": true, "poll_interval": "5m" }
}
```

[监听片段](../configs/approval/observation.fragment.example.json)必须合并进完整业务来源文件；不是可直接选中的 BUSINESS_CONFIG_FILE。省略 observation 或 enabled=false 不装配监听 worker。poll_interval 默认 5m，启用时范围 1m～24h；target_identity 必须明确选择 bridge/approval，所选原应用须有完整且唯一的凭证组，不能回退。每个进程只观察当前来源 Base/Table 下该应用的历史计划；切换来源/应用不会自动观察其他 registry，原批次仍可 check-approval 对账。

同一应用的 Base 与审批事件共用一条 SDK 长连接，独立审批应用另建连接。Base 仅轮询时也可以启用审批长连接。共用传输位于 `internal/feishu/events`，Base 附件与来源筛选留在 `internal/feishu/base/events`。审批事件不经过 Base 原始载荷日志，即使 FEISHU_LOG_RAW_EVENTS=true；没有新增公网 HTTP 接收端点。

飞书开发者后台需要为**所选应用**添加原生 `approval_instance` 事件，并申请 approval:approval:readonly 或 approval:approval。官方事件版本为 1.0：顶层 uuid 是事件 ID，event.uuid 才是创建 UUID；不是人员/任务范围的 approval.instance.status_changed_v4。还须订阅每个审批定义；配置监听不会代替平台事件/资源授权或自动写订阅。

```bash
# 先在业务文件指定已核对的 approval.template_code，并明确启用 observation。
# 仅显式执行才向所选应用订阅该模板，服务启动不执行此命令。
go run ./cmd/server subscribe-approval-events <与配置一致的审批Code>
```

命令先读取该模板，再调用一次官方 Subscribe；模板不可读、所选凭证缺失、参数不一致时停止，不创建状态目录、实例或附件。成功仅输出 subscription=accepted、event_delivery_verified=false；不代表已经收到事件。官方 1390007 同时描述“已订阅或已取消”，因此保留失败和 HTTP/code 诊断，不能据此确认订阅仍有效；不自动重发不确定请求。

收到应用身份匹配的事件后，先绑定本地已保存的 UUID/模板/实例 Code，在 2 秒队列上下文内持久化查询意图，再返回 SDK。没有 UUID 时只匹配已确认的实例 Code；建单响应尚未知时不猜关联，定时原 UUID 查询仍可补查。无关实例不建状态；同一事件重复不重新打开已处理意图，冲突 ID/实例映射不改绑。

后台只按原 UUID 查询并核对应用、模板和发起人，保存 verified 实例及追加历史，再确认意图。事件中的 status、时间和评论不作权威结果，token/原始载荷不入状态。查询失败、实例 Code 冲突或保存失败会保留 pending，重启/下一轮恢复；不建单、不重新 AI 审核。持久化确认与实例保存之间崩溃，只会再次查询。旧 registry 没有 notices 仍可读取，损坏关联则停止；状态文件保持 0600。

启动和定时轮询覆盖原应用全部已发送尝试，包括 finished，补查停机修改及迟到撤销；reserved、本地 abandoned/not_sent 不查询。原生 GET 的 APPROVED 加 reverted=true 保存为 reverted，PENDING 加 reverted=true 视为冲突；不会用事件的 OVERTIME 状态扩展官方 GET 枚举。终态不回退 pending，批准/拒绝/撤销/删除均不自动释放财务占用。每来源/应用有 OS worker 锁；当前单主机文件库保存全部历史意图并逐条查询，规模扩大时需要另行设计清理、限流和分页。

`approval-status <preparation_id>` 可以查看最新原实例状态及 verified/last_observed_at，仍不能当作 Base 或 Seal 交付成功。当前只持久化已核对状态，没有审批人/评论完整快照、Base 人工结果列、Seal manual-result 交付或结算释放。企业测试文件未启用此配置，监督服务未更新，未真实订阅或验收审批事件；实现与测试见[监听过程记录](progress/2026-10-05-approval-observation.md)。
