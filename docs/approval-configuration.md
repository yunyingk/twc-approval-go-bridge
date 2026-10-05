# 人工审批配置与只读预检

2026-10-05，业务来源仍由选中的 `BUSINESS_CONFIG_FILE` 决定。新增可选 `approval` 对象；当前企业配置没有启用它，既有 Anyreceipt/Seal 与自动送审设置不变。

```json
"approval": { "mode": "disabled" }
```

[关闭示例](../configs/approval/disabled.fragment.example.json)是配置片段，不是完整业务文件。`disabled` 允许保留不完整草稿；未知 JSON 属性仍拒绝。`manual` 表示显式人工选择的配置口径，**本阶段没有建单命令或自动建单装配**，不会仅因改此值创建审批。

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
