# 项目现状、可替换配置与易商卡对接参数

2026-10-08，Asia/Shanghai。本次只读核对仓库、私有配置存在性、运行端点、本地审核状态和线上字段结构，整理交接资料；没有开启新的人工审批开发、发起付费识别/审核或修改线上业务数据。

## 当前状态

代码基线是 `d48c186`（10 月 5 日收尾），本次开始时工作区干净。桥接服务与 FRP 两个 LaunchAgent 均为 running；本地 `127.0.0.1:18088` 和公网 `https://twc-approval.ying-qing.cn` 的 healthz、readyz、version 均返回 200，实际运行版本仍为 `public-debug-20261005-review-recovery`，使用排除 Anthropic 的构建。

本次 `check-business-config` 使用项目应用身份成功读取三个角色，核验交易流水 14 个字段、报销明细 20 个字段、台账 18 个字段。此项证明当前结构读取通过，不替代第三方写入权限或事件投递验收。本地 `review-status all` 为两份 Seal 历史审核 completed、delivered=true、decision=review，自动意图及来源收件队列均无待处理项；本次没有重新核对两份历史版本的当前业务事实。10 月 5 日最后核对时，两份已交付历史版本因新增支付输入/查重候选均已过时，见[审核恢复记录](2026-10-05-review-recovery.md)。

## 文件树与架构

```text
cmd/
  server/                 服务入口、配置装配及诊断命令
  approval-template/      独立审批模板工具
internal/
  app/recognition/        共用识别调度、轮询和交付
  app/review/             共用审核、版本隔离、自动触发和恢复
  app/approval/           保留的人工审批扩展，当前未启用
  core/invoice/           标准票据事实、精确金额和聚合
  core/review/            标准审核输入、结果和支付证据
  core/approval/          人工审批计划及结果证据
  core/dupcheck/          查重线索
  feishu/base/            表字段、附件、台账及结果读写
  feishu/events/          长连接传输及原生审批事件适配
  feishu/approval/        原生审批协议适配
  anyreceipt/            外部识别适配
  seal/                  外部审核、请求映射及回调适配
  anthropic/             独立的自有识别与审核适配
  state/                 单主机文件持久化
  config/                配置读取与校验
configs/                  业务来源、规则和模板示例
deploy/                   启动包装器、Docker、nginx 配置
external-api/             官方接口原文
docs/                     架构、运行和阶段验收资料
data/、tmp/               私有运行状态、日志与草稿，Git 忽略
```

主线：飞书明细附件 → 共用识别用例 → Anyreceipt（或自有模型）→ 台账 → 共用审核用例 → SealAI（或自有模型）→ 结果保存 → 专用 AI 列。两个 provider 独立选择；供应商协议留在适配层，共用用例使用 core 契约。旧 `anyreceipt/flow`、`anyreceipt/ledger` 等包装仍在代码中，属于阶段迁移未清理的技术债，不是未上线项目需要承担的长期兼容要求；具体调用核对与清理范围见下文。

SealAI 规则在 Seal 系统维护；只有自有审核加载本地规则。人工审批是另一条扩展，当前企业 JSON 没有 approval 对象，监督服务未部署后续人工审批代码，真实建单与人工结果/财务闭环尚未验收；保留提交供 review，收尾范围见[交付说明](../delivery-scope.md)。

## 未上线阶段的兼容债务核对

用户质疑为何尚未上线就保留兼容入口。本次基于 `84e7831` 核对代码调用：此前将业务编排迁入 `internal/app` 时保留了旧包装及旧测试，没有完成入口收敛；不能将“避免修改旧调用”作为长期保留依据。历史实现可由 Git 查询，无需让新系统一直运行旧路径。

| 遗留项 | 当前实际用途 | 清理范围 |
| --- | --- | --- |
| `internal/anyreceipt/flow`、`internal/anyreceipt/ledger` | 无生产代码导入；旧测试仍调用包装，台账测试在新目录已有对应覆盖 | 轮询/事件测试迁到共用识别与飞书事件适配层；移除旧包装与重复测试 |
| `internal/seal/review.Service`、`New` 和旧类型别名 | 旧包装只供本包测试调用；服务装配已直接使用 `app/review` 与 `Gateway` | 测试直接验证共用用例和真实 Gateway，移除包装；Gateway 仍承担真实 Seal 协议适配 |
| `submit-seal` | 仍可调用；固定 Seal，传入 `versioned=false`，不装配审核状态库和结果回写 | 统一使用 `submit-review` 与可配置提供方，消除绕过版本化和持久化的入口 |
| 未选择 `BUSINESS_CONFIG_FILE` 时的旧业务环境变量分支 | 仍可独立配置业务；当前监督服务使用完整 JSON，未走此分支 | 业务表/字段/提供方/触发方式收敛到业务 JSON；凭证、服务连接与部署参数继续由环境注入 |

外部识别/审核与自有模块的配置切换属于用户要求，应保留。供应商协议解码也有真实用途，例如 `receiptcompat` 目前被 Anyreceipt 客户端、模型客户端、台账和 Seal 映射调用；需要按实际数据契约分辨协议适配与历史数据支持，不能仅凭名称删除。已有私有状态与真实联调证据不因清理入口而删除。

本次完成调用核对和文档纠正，尚未删除代码、调整运行配置或更新监督服务；上述表格是待对齐的清理范围，不是已经完成的改动。人工审批和财务流程仍遵守本轮收尾边界。本次仅修改 Markdown，核验 Diff 和调用关系，不重跑业务测试或付费链路。

## 当前配置及替换项

业务文件为 [enterprise-test.json](../../configs/business/enterprise-test.json)，由 `BUSINESS_CONFIG_FILE` 选择。设置它后，文件决定表、字段、provider 和触发方式，对应旧业务环境变量不覆盖；凭证、端点和运行参数仍来自环境。一个进程使用一份业务文件，修改后重启生效。

| 项目 | 当前值 | 可以替换的内容 |
| --- | --- | --- |
| 表与字段 | 企业测试 Base 下的流水、员工明细、台账 | 各角色 Base/Table/Field ID；复制文档后需重新核验，不能只改 URL |
| 识别提供方 | recognition.provider=anyreceipt | anyreceipt / model / disabled |
| 识别触发 | recognition.trigger_mode=both | event / poll / both |
| 审核提供方 | review.provider=seal | seal / model，独立于识别方 |
| 送审时机 | review.trigger_mode=after_recognition | manual / after_recognition |
| 关联流水 | review.include_transactions=true | 开关；使用明细原生关联读取支付证据 |
| 修改重审 | 两个 resubmit 开关均 false，debounce=10s | 明细修改与流水/台账修改独立开启；真实新版本可能再次计费 |
| 审核输入/输出 | 消费事由、费用类目；7 个 AI 专用文本列 | context_fields / result_fields 的稳定字段映射 |
| 飞书身份 | 当前企业桥接应用 | FEISHU_APP_ID / FEISHU_APP_SECRET，换企业时核对应用与 Base 资源权限 |
| 外部服务 | Anyreceipt key、Seal 通道和回调已配置 | ANYRECEIPT_API_KEY、SEAL_DOCUMENT_URL、SEAL_BEARER_TOKEN、SEAL_CALLBACK_TOKEN |
| 自有模型 | 当前未启用，两个模型 key 均未配置 | 独立 RECEIPT_MODEL_* / REVIEW_MODEL_*；REVIEW_RULES_FILE 只供自有审核 |
| 运行参数 | 5 分钟轮询、baseline 首次扫描、文件状态库 | 轮询间隔、首次扫描策略、HTTP_ADDR、STATE_DIR 等 |

切换 model 前必须补齐所选模型的 key、端点、名称，并使用默认包含 Anthropic 的构建；当前 no_anthropic 二进制不会因改 provider 自动获得模型能力。自有识别只实现图片路径，PDF/多页及与外部系统的完整效果等价尚未验收。

凭证统一在私有 `.env`，`.env.public-debug` 保存调试参数，两者当前均为 0600。包装器按示例 → `.env` → `.env.public-debug` 注入；Go 程序不自动读取 dotenv。Base 类似一个工作簿，Table 类似其中的 Sheet，View 是同一 Table 的展示方式；当前未配置 View 范围。明细与台账必须同 Base；启用流水原生关联时，流水也须与明细同 Base。

## 已测试阶段

| 阶段 | 已有验收 |
| --- | --- |
| 10 月 4 日飞书事件 | 新企业副本真实修改/附件事件通过 WebSocket 抵达 |
| Anyreceipt | 真实附件识别，21 个原始输出保存，台账及双向关联回写，重复附件和重启核验 |
| SealAI | 显式与自动送审、真实公网回调、7 个 AI 字段回写、重复回调及同版本复用 |
| 10 月 5 日流水与恢复 | 真实关联流水读取、完整快照预览、诊断、部署核验；未新增带流水的付费审核样本 |
| 可选修改重审 | 隔离/契约测试通过，企业两个开关关闭，未做真实修改重审验收 |
| 自有模型 | 保留独立配置路径及部分测试；未完成企业主线替代验收 |
| 人工审批 | 客户端、准备/建单/恢复/观察等隔离测试；当前未启用，未验收真实人工流程 |

两种构建的完整测试和 vet 在 10 月 5 日收尾均通过。今天是只读现状核对及文档整理，没有重跑付费链路。

## 易商卡同事的连接参数

以“易商卡通过飞书 API 写入交易流水表”为对接用途：**一套飞书应用凭证，加 Base 与 Table 两个定位 ID，共 4 项**。只需当前企业应用，不提供旧审批应用或 Anyreceipt/Seal 凭证。

| 参数 | 值 | 含义 |
| --- | --- | --- |
| app_id | cli_aa4b551c9db85be4 | 当前企业飞书应用身份 |
| app_secret | 私有交接文件内，正文不记录 | App Secret，不是员工登录密码 |
| app_token（Base ID） | K5EhbDdEKa8wbJsTCpmcmeVwnHc | 整份多维表格 |
| table_id | tblr0rFnvNntoSYR | 其中的交易流水表 |

人工打开：[交易流水表](https://zyt-test.feishu.cn/base/K5EhbDdEKa8wbJsTCpmcmeVwnHc?table=tblr0rFnvNntoSYR&view=vew8ayvT8B)。链接的 `/base/…` 是 app_token，`table=…` 是 table_id；`view=…` 不是写入必填参数。app_id 与 app_token 是两个不同概念。

如果易商卡的配置界面只收 App ID、App Secret、完整表格链接三项，也可以由它从链接解析出两个定位 ID；前提是界面确实支持此解析。API 层仍需 app_token 和 table_id 两个值。

实际四参数（含 Secret）保存在被 Git 忽略的 `data/public-debug/yishangka-connection-20261008.json`，权限 0600，供用户自行单独转交；没有向同事发送消息。此文件为本次企业测试配置的快照，换企业/文档或重置密钥后需更新。

API 写入还需字段映射及正确数据类型，以及应用的记录写权限和目标 Base 编辑授权。今日只核验读取，没有替易商卡执行写入验收。当前三角色中，交易流水由第三方供数，桥接只读；个人明细由员工填写，台账由桥接写入。`source=third_party_webhook` 是供数说明，不表示本桥接已提供交易流水接收 Webhook。

官方协议：[应用身份取 token](https://open.feishu.cn/document/server-docs/authentication-management/access-token/tenant_access_token_internal)、[新增记录](https://open.feishu.cn/document/server-docs/docs/bitable-v1/app-table-record/create)。当前应用先用 app_id/app_secret 获取 tenant_access_token，再按 app_token/table_id 定位记录接口；不是将 App Secret 当作请求的 Bearer token。
