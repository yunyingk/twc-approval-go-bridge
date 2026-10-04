# 当前业务实现与交接

更新：2026-10-04。本文记录实际实现；[架构评审](architecture-review.md)保留为实施前的评审快照，[迁移任务](architecture-tasks.md)保留原有目标和后续验收依据。

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

```bash
# 默认 REVIEW_PROVIDER=seal；凭证及原有 RECEIPT_* 配置由环境注入。
go run ./cmd/server submit-review <个人报销明细记录ID>

# 自有审核另需 REVIEW_MODEL_* 与 REVIEW_RULES_FILE。
REVIEW_PROVIDER=model go run ./cmd/server submit-review <个人报销明细记录ID>
```

版本取决于业务来源、有效票据事实、原件内容、查重证据、配置的业务上下文及提供方版本；临时下载 URL、抓取时间不参与版本。新命令在调用审核前保存尝试，同一版本重跑复用已保存的提交结果。

Seal 提供方版本目前标识通道 URL，不锁定其规则集。Seal 实际使用最新发布规则；当前 Webhook 不提供规则版本选择或规则更新后强制重审协议。

提交请求失去响应时保留 `unknown`，不自动再次调用供应商；明确的 Seal 4xx 拒绝（408/429 除外）记录为 `failed`。两者都需要核对原因与供应商状态，当前不提供自动查询或自动解除该尝试的接口。

旧 `submit-seal` 继续固定使用 Seal，保留原 DocumentID 和未启用持久化的联调行为。正式接入版本和回调应使用 `submit-review`；不能把旧测试记录的回调当作新状态库中的已知任务。

### 业务上下文及结果回写

`REVIEW_CONTEXT_FIELD_IDS` 是当前个人报销明细表中「业务语义 → 稳定字段 ID」的 JSON 映射。读取出的文本值同时供两种审核器使用，Seal 中以对应语义作为 `TEXT` 字段 key。字段 ID 在实际读取时核验，不根据历史示例推断。

查重候选包含台账有效的票号、商户、票据类型、日期、金额和币种，Seal 请求同时描述查重证据范围。按票号检索没有候选不等于全量历史查重通过；候选也不等于已经报销或结算。

```text
REVIEW_CONTEXT_FIELD_IDS={"claimed_amount":"<已核对字段ID>","claimed_currency":"<已核对字段ID>","merchant":"<已核对字段ID>"}
REVIEW_RESULT_FIELD_IDS={"decision":"<字段ID>","document_id":"<字段ID>","revision":"<字段ID>","comment":"<字段ID>"}
```

结果映射支持 `decision`、`comment`、`document_id`、`revision`、`provider`、`external_id`、`url`。启用回写至少配置前三项；必须使用独立的类型 1 文本列，不能映射到人工审批或结算状态字段。上下文与结果字段不得重叠。未配置结果字段时，结果仍保存在本地状态中。`decision` 原样保存 `approve/reject/review`，表示 AI 建议。

结果先保存，再重新核对当前业务版本，最后回写。重复结果幂等；冲突结果拒绝；旧版本结果归档而不覆盖当前明细。服务启用结果交付后每 30 秒尝试补交付，不重新调用审核器。

来源适配器在读取前核对逻辑来源标识。切换企业或 Base 后，同一状态目录中的旧来源结果继续保存，不对当前租户读取、回写或重复重试失败。`completed` 表示已收到结果，`delivered=true` 才表示飞书交付完成，两者不是同一个阶段。

飞书读取和写入之间仍有并发修改窗口；当前没有平台条件更新或业务冻结锁。版本核对不是严格的事务锁，正式自动送审和金额锁定仍需明确提交信号与冻结机制。

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

`REVIEW_MODEL_API_KEY`、`REVIEW_MODEL_BASE_URL`、`REVIEW_MODEL_NAME` 与识别模型配置独立；`REVIEW_RULES_FILE` 只供此路径使用。示例见 [本地规则文件](../configs/review/rules.example.json)。缺少要求的上下文直接输出待复核，默认将模型批准/驳回建议转换为人工复核；只有经确认的本地规则显式允许自动决定时才保留模型决定。

自有识别支持图片、纯 JSON 和单个 Markdown JSON 围栏；拒绝被 token 上限截断的响应，发现多票标记时明确报错。PDF/多页拆分尚未实现。自有审核目前提供基于结构化事实的受控语义评审，不代表已等价覆盖 Seal 的验真、历史占用、外部数据查询与全部规则。

`no_anthropic` 构建可运行外部主线，调用自有识别或自有审核时明确返回编译排除错误。供应商真实效果与费用尚未重新评估。

## 仍需明确的业务工作

1. 业务提交/冻结信号；自动送审时机、撤回重提和附件删除后的生命周期。
2. 交易流水的关联与事实映射。当前上下文映射只读取报销明细；没有实现跨表交易、授权、汇率或历史结算事实查询。
3. Seal 发票修正协议。官方备份明确：同一 `sourceInvoiceId` 改核心事实会 conflict，不同来源 ID 提交相同票号也会 conflict。因此保留稳定来源 ID，不能靠更换 ID 绕过冲突；新单据版本并不提供 Seal 发票事实原地更新能力。
4. 飞书分组审批实例、跨租户人员映射、人工结果及 Seal `manual-result` 同步；当前仍只有模板工具和实例接口草稿。
5. 历史占用、合法分摊、结算、记录可见性与锁定；真实字段和平台权限核对后实现。
6. 正式环境结果字段、持久卷与 HTTPS 入口配置，并进行项目应用身份下的真实验收。

后续实现按这些业务前置条件继续；不再以供应商目录容纳共用流程，也不要求 Seal 主线维护本地规则文件。
