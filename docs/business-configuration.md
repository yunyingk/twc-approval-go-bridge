# 业务来源与配置

更新：2026-10-04。业务配置文件负责表、字段、提供方和触发方式；凭证继续由私有 `.env` 或进程环境注入。当前生效文件是 [`configs/business/enterprise-test.json`](../configs/business/enterprise-test.json)。

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

## 配置选择和优先级

```text
BUSINESS_CONFIG_FILE=configs/business/enterprise-test.json
```

设置后，文件完整决定以下业务项：

- 三个表角色及其字段映射；`detail_id` 可省略，缺省使用飞书记录 ID。
- `recognition.provider=anyreceipt|model|disabled` 与 `trigger_mode=event|poll|both`。
- `review.provider=seal|model` 与 `trigger_mode=manual|after_recognition`。
- `review.include_transactions=true|false`，默认 `false` 保留原审核版本和读取行为；启用后要求明细的 `transaction_relation` 及流水的交易号、金额、币种、商户和时间字段。
- `review.resubmit_on_detail_change=true|false`，默认关闭；启用需使用 `after_recognition`。`review.change_debounce` 默认 `10s`，允许 `1s` 至 `10m`，用于合并连续编辑。
- `review.resubmit_on_source_change=true|false`，默认关闭，与明细开关独立；在 `after_recognition` 下监听台账及已启用的交易流水事实，使用同一等待时间与版本化审核。
- `review.context_fields` 与 `review.result_fields`，两者均属于报销明细表。

对应的旧 `RECEIPT_*` Base/Table/Field、提供方和触发方式，以及旧 `REVIEW_PROVIDER`、`REVIEW_TRIGGER_MODE`、上下文和结果映射全部忽略，防止把两套文档配置拼起来。未设置 `BUSINESS_CONFIG_FILE` 时，原环境变量模式继续兼容。

凭证、HTTP 地址、日志、状态目录、轮询间隔/首次扫描策略、模型端点和名称、Seal 通道 URL、本地规则路径继续由运行环境提供。程序不自动读取 `.env`；本机 [`deploy/run-local-debug.py`](../deploy/run-local-debug.py)负责注入。Seal 规则仍维护在 SealAI；`REVIEW_RULES_FILE` 只用于自有审核。

当前配置保留用户选择：Anyreceipt 识别、Seal 审核、`after_recognition` 自动送审，并于 2026-10-05 启用 `review.include_transactions=true`。以后改为人工提交，只改文件中的 `review.trigger_mode`；更换识别或审核提供方，各自改对应 `provider` 并提供凭证，模型路径还需支持 Anthropic 的构建。

未知 JSON 属性、无效版本、重复物理表、重复字段映射、覆盖员工输入的 AI 结果列等会在启动前报错。基础结构校验并不替代线上权限、字段类型和关联目标核验。

## 关联流水审核与预览

启用流水后，审核请求包含独立的 `transactions` 证据：来源表范围、关联记录 ID、交易号、商户、UTC 交易时间、原币金额/币种及可选记账 CNY 金额、国家、交易类型、流水状态。原币金额和 CNY 金额分别保存，不推导汇率、不相加跨币种金额，也不创建发票占用或分摊。

服务用关联中的 `record_ids` 定位记录，不使用显示流水号。金额保留精确十进制字符串，零和缺失不同；格式不明确的金额、非三字母币种或无效时间保存为资料问题及原值，不变成猜测事实。空关联会显式标注 `missing_transaction_relation`；原生单关联意外返回多条时保留各条并标注问题，不取第一条冒充完整依据。合法多关联保留各条，不推断费用分摊。

权限错误、配置字段或关联目标错误、记录不可读和未知关联格式会阻止准备和提交，不能当成成功读取后的资料缺失。第三方流水始终不由桥接服务修改。

Seal 接收完整证据 JSON 和以 `bridge_transaction_` 为前缀的文本字段，继续执行 Seal 系统发布的规则；不保证旧规则已采用新增字段。自有审核接收相同证据，资料质量存在问题时直接转人工复核，其余判断按本地规则进行。审核上下文不得使用生成证据的保留键。

```bash
# 注入运行环境后预览同一套真实读取、有效票据事实和版本；不送审、不回写
go run ./cmd/server preview-review <报销明细记录ID>
# 本机注入私有运行环境的入口
python3 deploy/run-local-debug.py preview-review <报销明细记录ID>
```

预览需要已有完整 OCR 台账，读取并校验原件；输出不包含原件二进制、下载 URL 或原始 OCR，但包含业务上下文及流水事实，只适合私有调试。它不创建审核尝试，不调用 OCR、Seal 或模型。

流水事实、来源和资料质量问题纳入审核版本。重复准备同一快照不重复送审；回写前读取当前事实，旧结果只归档，不覆盖新数据。启用开关会增加审核依据，因此在途旧版本可能失效，已识别任务和 OCR 基线不变。企业配置当前仍仅在识别完成时自动送审；直接流水事实变化可另行选择下方的来源修改开关。多次 API 读取与回写不是原子事务，也未建立业务冻结锁。

## 可选的明细修改重审

```json
"review": {
  "provider": "seal",
  "trigger_mode": "after_recognition",
  "resubmit_on_detail_change": true,
  "change_debounce": "10s"
}
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

1. 复制当前 JSON 为另一份，例如 `configs/business/company-b.json`，填写新的名称和三组 Base/Table/Field ID。配置文件允许入 Git，不能加入密钥或真实交易内容。
2. 给当前应用授予新 Base 的资源访问，核对应用权限；换企业应用时也更换环境中的 App ID/Secret。长连接事件还需要新 Base 的订阅。
3. 在准备运行的环境中选择新文件，执行只读核验：

   ```bash
   BUSINESS_CONFIG_FILE=configs/business/company-b.json \
     go run ./cmd/server check-business-config
   ```

   需要注入应用及选中提供方的运行凭证。本机可用 `python3 deploy/run-local-debug.py check-business-config` 检查已选择的文件。命令使用项目应用身份，仅获取字段结构，检查配置的字段存在、附件/台账/AI 结果类型以及已配置的业务关联目标；不读业务记录、不调用 OCR/Seal、不修改表。成功会输出三个角色的核验摘要；读取权限通过不代表写入权限或事件订阅已验收。
4. 核验通过后更新 `BUSINESS_CONFIG_FILE` 并重启，按新的企业资源实际验证事件和回写。容器需将 JSON 目录以只读方式挂载到选中的容器路径；不要将私有 `.env` 烘焙进镜像。

配置名称和文件路径不参与识别去重或审核版本；同一组有效绑定迁移到 JSON 后继续使用已有基线和任务。真正更换 Base/Table/Field 或相关识别配置会进入新的任务范围。保留 `STATE_DIR`，不要为切换配置删除已有状态；新范围的首次扫描按 `RECEIPT_POLL_STARTUP` 执行。
