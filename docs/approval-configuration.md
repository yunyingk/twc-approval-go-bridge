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

人员映射校验格式及完整性，不代替目标企业的人员/发起权限核验。附件列目前只验证真实 Base 引用并报告 approval_upload_required，上传及复用尚未实现；不能将 Base token 当审批 file code。配置存在不代表完整表单可用。时区数据编入程序以支持静态镜像，未配置日期规则时不默认月份或时区。

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
