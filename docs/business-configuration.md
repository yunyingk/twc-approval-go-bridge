# 业务来源与配置

更新：2026-10-08。项目运行只读取一份私有 `config.toml`（0600、Git 忽略），不合并 `.env`、调试参数或旧业务配置。模板及自有审核规则独立保存，主配置仅按需引用规则。仓库仅保留[完整脱敏示例](../configs/config.example.toml)。配置迁移不更换业务资源或状态目录。

## Base、Table 与 View

可以把一份飞书多维表格文档看作一个工作簿：

```text
Base（多维表格文档，app_token / base_token）
├── Table：交易流水表（table_id）
├── Table：个人报销明细（table_id）
│   ├── Field：上传发票、消费事由等列（field_id）
│   └── Record：某员工的一条报销明细（record_id）
└── Table：发票台账（table_id）
    └── View：同一张表的筛选、排序和展示方式（view_id）
```

Table 类似普通表格的 Sheet；View 是同一张 Table 的另一种展示，不是另一份数据。新建或复制多维表格文档，会得到另一套 Base/Table/Field 标识，不能只替换网址中的一个 ID，也不能假定字段 ID 都保留。字段重命名通常继续使用原 ID。

当前链接中 `/base/K5EhbDdEKa8wbJsTCpmcmeVwnHc` 指向 Base，`table=tblr0rFnvNntoSYR` 指向「交易流水表」，`view=vew8ayvT8B` 指向该表的某个视图。服务按 Base、Table 和附件 Field 处理记录，不采用链接中的视图筛选条件；当前没有 View 范围配置。

官方接口分别以 [Base 下的数据表](https://open.feishu.cn/document/server-docs/docs/bitable-v1/app-table/list)和[数据表下的视图](https://open.feishu.cn/document/server-docs/docs/bitable-v1/app-table-view/list)组织资源。

## 显式业务角色

业务角色稳定，实际文档、表、列可以更换：

| 配置角色 | 当前表 | 数据来源 | 桥接服务的实际行为 |
| --- | --- | --- | --- |
| `transactions` | 交易流水表 | 第三方 Webhook 导入 | 只读；启用 `review.include_transactions` 后按明细关联读取真实支付事实 |
| `reimbursement_details` | 个人报销明细 | 员工填写 | 读取附件、明细编号和审核上下文；结果只写 `review.result_fields` 中的专用 AI 列 |
| `invoice_ledger` | 发票台账 | 桥接服务识别结果 | 按来源键写入票据事实、完整原始 JSON 和明细关联；保留已有人工事实 |

每个角色独立声明 `base_token`、`table_id`、`source`、`access` 和「业务语义 → 字段 ID」映射。`name` 是说明文字；`source` 记录谁供数，**不会自动创建 Webhook 接收接口**；`access` 表示服务的使用约定，飞书仍按应用权限和资源授权校验。

当前三张业务表都位于同一个 Base。**报销明细与发票台账必须同 Base**，因为当前台账适配器与原生关联按此范围工作；配置加载器拒绝跨 Base 台账。流水表可以声明其他 Base 的只读来源；启用关联流水参与审核时，流水与明细也须同 Base，必须使用实际指向配置流水表的原生关联字段。来源声明不能替代事实读取开关，也不表示人工审批或结算已经接通。

当前一个进程运行一份配置，编辑后重启生效；没有同时监听多套业务来源或热更新能力。未来接入更多来源时，继续增加角色适配和编排，不能把额外表 ID 塞入供应商模块。

## 主配置入口

默认读取工作目录下的 `config.toml`；只有 `CONFIG_FILE` 可以指定这份文件的其他路径。文件不存在、TOML 无效、未知属性或绑定不成立时直接报错；没有环境变量后备或配置叠加。

| 同一文件内的部分 | 内容 |
| --- | --- |
| `runtime` | HTTP 地址、日志级别、停止等待、状态目录 |
| `feishu` | 应用 ID/Secret、长连接事件类型与日志开关、可选独立审批应用 |
| `tables` | 流水、员工明细、台账三个角色的 Base/Table/Field ID、来源与访问约定 |
| `recognition` | `provider=anyreceipt|model|disabled`、`trigger_mode=event|poll|both`、轮询间隔及首次扫描方式、独立模型连接 |
| `anyreceipt` | 识别 API key |
| `seal` | 单据通道 URL、Bearer 密钥、回调鉴权 token |
| `review` | `provider=seal|model`、`trigger_mode=manual|after_recognition`、流水/修改重审开关、上下文与 AI 专用字段、独立模型连接及独立自有规则文件路径 |
| `approval` | 可选人工审批配置；当前本机没有启用 |

识别与审核分别选择提供方。Seal 规则在 SealAI 系统维护；`review.rules_file` 只用于自有模型。两个 `model` 对象分别保存 `api_key`、`base_url` 和 `name`，不跨能力复用密钥。切换模型还需包含 Anthropic 的构建；修改 TOML 不能向 `no_anthropic` 二进制加入 SDK。

`recognition.poll_startup=baseline|process` 决定首次扫描只记录基线还是处理已有附件；默认 baseline。`review.result_fields` 支持 decision、comment、document_id、revision、provider、external_id、url，必须绑定独立 AI 文本列，不得覆盖员工输入。自动审核要求识别、台账交付及 decision/document_id/revision 字段；Seal 自动审核还要求回调 token。

实际密钥只放私有 config.toml，不提交到 Git，不输出完整配置。旧配置注释与模板说明保存在[迁移前记录](progress/2026-10-08-previous-config-notes.md)，不是运行入口。

## 独立模板与自有规则

- `templates/feishu/approval-template.example.json`：一次性创建飞书原生审批模板的请求。由 `approval-template -file` 读取，运行服务不加载。
- `rules/review.example.json`：自有审核示例规则。只有 `review.provider = "model"` 才读取 `review.rules_file`；相对路径以主 TOML 所在目录为准。SealAI 路径不要求文件存在。

主配置顺序为识别/审核提供方、连接凭证、运行参数、可选模型连接、表及字段映射。TOML 支持 `#` 注释和分节；重复键/节、错误类型及未知结构字段会报错。字段映射中的语义名称仍按现有业务校验处理，不是完整白名单。旧 JSON 主配置不再受新加载器支持，没有兼容回退。当前监督服务仍运行上一阶段二进制与私有 JSON，尚未部署本次改动，见[交接记录](progress/2026-10-08-config-toml.md)。

## 可选的明细修改重审

```toml
[review]
provider = "seal"
trigger_mode = "after_recognition"
resubmit_on_detail_change = true
change_debounce = "10s"
```

以上为局部示例，需保留完整文件的上下文、结果字段及其他配置。企业测试文件当前显式关闭这个开关，维持已选择的识别完成触发方式。

启用后监听明细附件、明细编号、已配置的审核上下文，以及启用流水时的流水关联。只有事件实际包含且发生变化的输入字段才触发；AI 专用结果、人工审批、锁定和派生关联列不触发。即使识别采用纯轮询，修改重审仍需要飞书长连接事件订阅。

事件处理只保存意图，不读取附件或调用审核方；后台等到最后一次编辑的等待时间结束后，读取当前完整事实，复用共同版本化送审。期限、事件去重和任务代数持久化，重启保留等待时间，审核过程中出现的新编辑不会被旧任务确认丢弃。每条明细保留最近 64 个修改事件 ID；更旧事件重放仍受审核版本去重保护。事件相同或事实未变不会新增付费审核；确实形成新版本时可能再次计费。

新附件仍须先完成 OCR 台账；删除部分附件会基于剩余附件准备新版本。附件全部清空或明细确已删除时，结束待送审意图，保留旧台账和审核历史；迟到结果保存后视为旧结果，不覆盖当前明细。权限或网络错误、关联流水不可读以及台账不齐不会被误判为删除。

该开关只覆盖报销明细自己的修改事件。直接修改第三方流水或发票台账使用下面独立的来源开关；停机期间缺失的明细编辑事件也没有补扫机制。已显示的旧 AI 建议不会立即清空，须结合版本判断有效性。业务冻结、撤销供应商单据和人工审批仍是独立后续工作。

## 可选的来源修改联动

`review.resubmit_on_source_change=true` 在已配置台账及启用的交易流水上监听有效审核输入，独立于 `resubmit_on_detail_change`。企业文件当前两个开关均关闭。来源开关需要 `after_recognition`、台账票号字段和 Base 事件订阅；识别采用纯轮询时也会启动长连接。

来源事件先保存独立收件记录，后台依据同一业务范围内已经保存的审核快照定位受影响明细：流水按来源表及关联记录 ID；台账按本票的台账记录 ID、历史候选记录 ID，以及事件中的修改前/后票号。新增同票号的台账行也可使已有明细重新准备查重证据；不能只更新新增票据自己的报销行。

事件缺少票号线索或票号格式未知的台账新增/删除，保守地重新准备当前范围内已有审核的明细，不把未知内容当作票号、金额或审核结论。首次审核可能尚未保存依赖快照，因此同时保留当前等待/执行中的自动任务；这可能增加读取，但相同业务版本仍不再次付费。历史依赖也可能增加一次读取，不会替代当前事实。

启用流水前保存的审核没有支付依赖快照，不能据此断言该明细从未关联流水。首次收到流水变化时会重新准备这类旧明细；一旦保存过明确支付证据，历史空快照就不再造成扩大读取，后续按记录依赖定位。

有效字段之外的申领状态、锁定、人员、派生概要和原生反向关联不触发。关联的变化使用明细开关；不修改第三方流水。后台重新读取当前资料，使用同一版本去重、结果隔离和编辑等待时间。

一份事件向多条明细排队时，只在全部意图落盘后确认收件记录。部分失败后恢复，已排队明细复用事件标识和首次接收时间，不延长原等待期限；重复投递不会重开已完成记录。更换来源表时旧收件记录保留，不能重定向到新表。与审核/识别状态使用不同 JSON 范围字段，避免把流水记录误当作待审明细。

开关关闭期间不补建事件；停机丢失的事件仍需补扫。人工发票事实修正可能触发 Seal 稳定 `sourceInvoiceId` 的核心字段冲突，桥接保留 `failed`，不改发票 ID 绕过原有占用/查重；该外部异常处理仍需业务对账。多次读取、来源变更、人工显式提交与回写之间仍有并发窗口，来源联动不是业务冻结事务。过程和验收范围见[来源联动记录](progress/2026-10-05-source-review-changes.md)。

## 换企业或文档副本

人工审批另有可选 `approval` 配置，字段绑定、目标身份、日期分组与 AI 结果门禁说明见[人工审批配置与预检](approval-configuration.md)。不配置时原识别/Seal 链路继续运行；它不替 Seal 维护规则，也不自动发起人工审批。

1. 在这份 `config.toml` 中更新企业应用凭证及三组 Base/Table/Field ID。配置含密钥，不进入 Git；先保留私有备份。
2. 授予应用访问新 Base 的资源权限，核对应用权限及目标 Base 的事件订阅；服务继续使用应用身份。
3. 执行 `go run ./cmd/server check-business-config`，或在所选 CONFIG_FILE 下执行 `go run ./cmd/server check-business-config`。命令只获取字段结构，核对字段类型和关联目标，不读取业务记录、不调用 OCR/Seal、不修改表。读取通过不代表写入或事件验收通过。
4. 核验通过后重启服务，实际验证事件与回写。容器只读挂载同一份 TOML 至 `/config.toml`，`runtime.state_dir` 使用 `/data`；配置文件须可被容器运行 UID 读取，私有配置不进入镜像。

配置名称和路径不参与识别去重或审核版本；迁移配置格式不会重建基线或任务。更换实际 Base/Table/Field 会进入新的业务范围，首次扫描按 recognition.poll_startup 执行；保留 runtime.state_dir 及已有状态，不为切换配置删除历史证据。
