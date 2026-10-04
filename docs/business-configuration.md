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
- `review.context_fields` 与 `review.result_fields`，两者均属于报销明细表。

对应的旧 `RECEIPT_*` Base/Table/Field、提供方和触发方式，以及旧 `REVIEW_PROVIDER`、`REVIEW_TRIGGER_MODE`、上下文和结果映射全部忽略，防止把两套文档配置拼起来。未设置 `BUSINESS_CONFIG_FILE` 时，原环境变量模式继续兼容。

凭证、HTTP 地址、日志、状态目录、轮询间隔/首次扫描策略、模型端点和名称、Seal 通道 URL、本地规则路径继续由运行环境提供。程序不自动读取 `.env`；本机 [`deploy/run-local-debug.py`](../deploy/run-local-debug.py)负责注入。Seal 规则仍维护在 SealAI；`REVIEW_RULES_FILE` 只用于自有审核。

当前配置保留用户选择：Anyreceipt 识别、Seal 审核、`after_recognition` 自动送审。以后改为人工提交，只改文件中的 `review.trigger_mode`；更换识别或审核提供方，各自改对应 `provider` 并提供凭证，模型路径还需支持 Anthropic 的构建。

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

流水事实、来源和资料质量问题纳入审核版本。重复准备同一快照不重复送审；回写前读取当前事实，旧结果只归档，不覆盖新数据。启用开关会增加审核依据，因此在途旧版本可能失效，已识别任务和 OCR 基线不变。**流水字段变化本身暂不自动发起新审核**；当前自动触发仍为识别完成，其他修改需显式提交或后续业务触发能力。多次 API 读取与回写不是原子事务，也未建立业务冻结锁。

## 换企业或文档副本

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
