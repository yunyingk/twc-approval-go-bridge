# twc-approval-go-bridge

影视飓风的飞书、Seal 与海外票据识别桥接服务。基础服务、飞书事件长连接、票据识别回写和独立的 SealAI 审核提交路径已有可运行代码。

配置加载、结构化日志、HTTP 生命周期、健康检查、持久化识别任务、版本化审核提交和 AI 结果接收/回写已经具备。企业副本已通过真实 Anyreceipt 识别、显式 Seal 送审、公网回调及七个专用 AI 字段回写验收，见[开发与验收记录](docs/progress/2026-10-04-seal-review.md)。送审时机已配置化；业务冻结、飞书审批实例、跨单据历史占用与高级权限能力仍待完善。

Anyreceipt、SealAI 与飞书审批的接口原文保存在仓库顶层的 [`external-api/`](external-api/README.md)，由 Git 管理；该目录只保存官方原始资料，不承载运行配置。

后续扩展参考：[架构评审：外部能力主线与自有能力替换](docs/architecture-review.md)及[迁移任务与验收](docs/architecture-tasks.md)。两份文件保留实施前评审；当前代码、配置、命令与剩余业务边界见[运行与交接说明](docs/runtime-and-handoff.md)。识别与审核独立切换，Seal 规则继续在 SealAI 系统维护。

## 快速开始

需要 Go 1.24.13。项目通过 `go.mod` 的 `toolchain` 指令、CI 和 Docker 构建镜像统一固定到该版本。

```bash
make test
make vet
make build
./bin/twc-approval-go-bridge
```

服务默认监听 `:8080`，可以通过环境变量覆盖配置：

```bash
HTTP_ADDR=:9090 LOG_LEVEL=debug SHUTDOWN_TIMEOUT=15s make run
```

业务来源推荐用显式文件配置。设置 `BUSINESS_CONFIG_FILE=configs/business/enterprise-test.json` 后，流水、员工明细、发票台账各自的 Base/Table/字段，以及识别和审核提供方、触发方式由该文件整套决定；对应旧环境变量不再覆盖。凭证继续由环境注入。Base/Table/View 概念、更换文档步骤和只读核验命令见[业务来源与配置](docs/business-configuration.md)。当前配置采用识别完成自动送审；明细与台账仍须在同一个 Base。

企业配置已启用 `review.include_transactions=true`：按明细原生关联只读获取交易流水，精确金额、币种、商户、时间及资料质量问题供 Seal 与自有审核共同使用并参与审核版本。`preview-review <明细记录ID>` 可预览实际快照，复用已有 OCR 台账而不送审或回写。真实读取、两份完整快照预览及启用验证见[交易流水开发记录](docs/progress/2026-10-05-transaction-review.md)；本次没有新增付费审核验收。

## 基础端点

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/healthz` | 进程存活检查 |
| GET | `/readyz` | 服务就绪检查 |
| GET | `/version` | 构建版本 |
| POST | `/seal/callback/mock` | 仅供本机模拟 Seal 审核结果；不作业务回写 |

mock 回调只接受来自本机的请求并返回 `{ "success": true }`；它不是公网回调。设置 `SEAL_CALLBACK_TOKEN` 后可接收 `POST /seal/callback/<token>` 并应用已知版本的审核结果；需在 Seal 配置对应的 HTTPS URL。测试租户已通过[本机公网入口](docs/public-debug.md)验证真实回调与重复投递，配置与鉴权约定见[运行说明](docs/runtime-and-handoff.md)。

## Docker

```bash
make docker-build
make compose-up
```

也可以直接构建：

```bash
docker build --file deploy/Dockerfile --tag twc-approval-go-bridge:local .
```

镜像使用多阶段构建，运行时只包含静态 Go 二进制。

## Feishu 长连接适配器

项目已加入 `github.com/larksuite/oapi-sdk-go/v3 v3.12.0`，并提供可选的长连接监听器。只有同时配置 `FEISHU_APP_ID` 和 `FEISHU_APP_SECRET` 时才会启动；未配置时服务仍可作为普通 HTTP 服务运行。

```bash
FEISHU_APP_ID=cli_xxx \
FEISHU_APP_SECRET=xxx \
FEISHU_EVENT_TYPE=drive.file.bitable_record_changed_v1 \
go run ./cmd/server
```

未配置识别服务时，监听器只负责建立连接、注册事件类型并接收原始事件。默认日志记录事件 ID、事件类型和载荷大小；调试原始载荷时可以临时设置 `FEISHU_LOG_RAW_EVENTS=true`。

配置 `RECEIPT_PROVIDER=anyreceipt` 或 `RECEIPT_PROVIDER=model` 后，会启用附件识别链路。服务按 `RECEIPT_BASE_TOKEN`、`RECEIPT_TABLE_ID`、`RECEIPT_ATTACHMENT_FIELD_ID` 筛选记录变更事件，只在附件字段新增内容时读取该行。字段用稳定 ID 定位，改名不影响配置。事件快速进入有界队列；后台校验下载文件的实际内容类型，仅允许 PDF、JPEG、PNG、GIF、WebP。识别结果（包括供应商返回的所有字段和原始响应）交给 `ResultHandler`。配置 `RECEIPT_LEDGER_TABLE_ID` 和 `RECEIPT_LEDGER_FIELD_IDS` 后，结果处理接口会写入「发票台账」；不配置台账时只记录处理摘要。应用还需在飞书开放平台的「事件与回调」中选择长连接接收并添加「多维表格记录变更」事件，同时通过云文档订阅接口订阅目标 Base；仅建立长连接不会自动订阅事件。

`RECEIPT_TRIGGER_MODE` 可选 `event`、`poll` 或 `both`（默认）；两条路径共用附件读取、识别队列和持久化交付状态。`poll` 每隔 `RECEIPT_POLL_INTERVAL`（默认 `5m`）只扫描配置的 Base 中指定的「个人报销明细」Table，并按附件字段 ID 定位字段。`RECEIPT_POLL_STARTUP=baseline`（默认）表示首次扫描仅记录已有附件，后续只处理新增 token；设为 `process` 则首次扫描也处理已有附件。轮询不依赖事件投递，但仍需应用对目标 Base 的读取权限。当前基线、任务及识别结果保存到 `STATE_DIR`（默认 `data`）；重启保留原基线并恢复未交付任务，避免因回写失败重复 OCR。同一来源只允许一个识别 worker，当前文件状态库适用于单主机持久卷。识别失败的附件会由恢复任务或后续扫描重试。

2026-10-04 已在新的企业测试应用和 Base 中验证真实 WebSocket 事件投递，以及附件事件触发 Anyreceipt、21 个识别输出、台账回写与双向关联。需同时开通后台应用身份和用户身份的 `bitable:app` 权限；服务仍只使用应用身份，不需要用户 OAuth。配置及验收见[飞书事件联调](docs/feishu-event-verification.md)和[企业 OCR 验收](docs/anyreceipt-enterprise-verification.md)。运行时也可设 `RECEIPT_TRIGGER_MODE=poll` 只使用轮询。主线路径使用 `RECEIPT_PROVIDER=anyreceipt`，自有多模态模型是可选切换项。项目凭证统一放入私有 `.env`；程序本身不自动加载该文件，本机调试使用[运行包装器](deploy/run-local-debug.py)注入配置。

订阅范围是整个 Base 的记录变更，并非单个字段：任意数据表的行新增、修改、删除都可能推送 `drive.file.bitable_record_changed_v1`。服务收到后才过滤 Base ID、数据表 ID 和附件字段 ID；修改「消费事由」或第三方「交易流水表」不会触发识别，只有「个人报销明细」的「发票附件」新增文件才进入识别队列。字段本身改名属于另一类字段变更事件。长连接方式无需配置事件加密策略；向开发者服务器推送的 Webhook 方式才涉及该配置。

旧个人版测试 Base `BgNkbW1RKavyaPsYD6acNZPFnUb` 的业务定位（2026-09-29 历史联调）：`交易流水表`（`tbloRvQFZNLugLB0`）由第三方写入，本服务只读取；`个人报销明细`（`tblMC3p2Vm2Mwuh9`）是员工补充票据附件、触发识别的来源，附件字段是 `发票附件`（`fldnKx8Uzo`）；`发票台账`（`tblKwQ4NK6t4G69S`）接收识别结果。台账新增了「识别来源键」（`fldrkOhogY`）和「识别原始JSON」（`fldeECmr7u`）：按来源记录 ID 与附件 token 查找并新增或更新同一行，完整供应商响应保存在原始 JSON 字段。依照[测试流程](https://xcn4e0le81w4.feishu.cn/wiki/UF7ZwwdtKiwEEikHI19c2VkOndb)的 4.1 对照，「发票唯一键」写 Anyreceipt 的 `traceId`；可选模型结果无 `traceId` 时用 `OCR-` 加来源键哈希。台账已补齐发票摘要、票据类型、业务分类、买方、税率、国家、AI 消费概要；原有「税前金额」「应付金额」「GST税额」已原地改名为「不含税金额」「含税金额」「税额」，保留历史值。每张台账发票关联一条「个人报销明细」，明细一侧允许关联多张发票；字段 ID 映射见 `RECEIPT_LEDGER_FIELD_IDS`。汇率、人民币金额、白名单、报销状态、识别状态等待确认项不从 OCR 结果推断或覆盖。OCR 失败只记录错误并等待轮询重试，不创建「识别失败」台账行。

## SealAI 审核提交

`REVIEW_TRIGGER_MODE=after_recognition` 可在识别结果写台账后自动送审；`manual` 保留显式命令。两种路径都保证同一行所有附件识别并写入台账后，才聚合成一份审核单据。自动意图与版本状态持久化，重复触发复用已有版本；配置和恢复见[自动送审记录](docs/progress/2026-10-04-automatic-review.md)。显式 Seal 命令仍可使用：

业务文件的 `review.resubmit_on_detail_change=true` 可启用明细修改重审，`change_debounce` 配置连续编辑的合并等待时间，默认 `10s`。企业文件当前关闭此开关。AI 回写不触发重审；全部附件清空或明细删除会结束待送审，旧结果仍保留。范围、费用和恢复边界见[配置说明](docs/business-configuration.md#可选的明细修改重审)。

```bash
# 运行环境还需注入 FEISHU_APP_ID/SECRET、RECEIPT_* 表字段配置和 Seal Bearer 密钥
SEAL_DOCUMENT_URL='https://mediastorm-test.sealai.cc/api/v1/integrations/webhook/wh_1790692906316_6z2eao6/document' \
SEAL_BEARER_TOKEN='<运行环境注入>' \
go run ./cmd/server submit-seal <个人报销明细记录ID>
```

读取范围固定为配置中的「个人报销明细」和「发票台账」，不修改第三方「交易流水表」。服务逐张核对台账来源键，缺少 OCR 结果时不提交半份单据；按票号查找历史台账候选，把查重标记、完整 OCR JSON 和每张原始附件放入同一 SealAI 请求。查重模块只产生证据，不自动审批。SealAI 上传接口成功时返回附件 ID 和文件对象；普通 `ATTACHMENT` 字段使用文件对象，结构化发票的 `evidenceAttachmentId` 使用 ID。上传网络或响应协议异常直接报错，不再用文本附件 ID 替代。结构化发票仅在票号、币种、金额、买卖方和 Seal 附件 ID 均齐全时加入；缺项时仍提交属性和原件供审核。

首次测试单据只含文本附件 ID，SealAI 对同一 `documentId` 的后续成功响应没有更新该审核记录。用新的测试单据编号 `OCR-TEST-20260929-175848-ATTACHMENT-PROOF` 验证后，审核页明确显示「发票附件：1 个文件」和原文件下载按钮，审核过程显示解析 1 个附件。该样本 OCR 缺币种，因此结构化发票数为 0。浏览器插件阻止了跳转到 OSS 下载域，故没有把浏览器端成功打开图片作为验证结论。

聚合位于独立的 `internal/core/invoice/aggregate/aggregate.go`，结构不依赖 Anyreceipt 或 SealAI；`internal/seal/mapper/` 负责 Seal 协议转换。自有模型可复用 `internal/core/invoice` 的识别接口与聚合结构。历史测试通道的回调地址曾指向本地 mock；SealAI 服务器不能连接到开发者电脑的 `127.0.0.1`。新入口 `submit-review` 及识别完成自动触发共用版本化送审、密钥 URL 结果接收和 AI 结果回写；企业测试专用列已通过真实验收，业务冻结仍待完善。旧 `submit-seal` 保持联调兼容语义，正式链路使用新入口。

Anyreceipt 始终编入，启用识别时使用 `RECEIPT_PROVIDER=anyreceipt` 和 `ANYRECEIPT_API_KEY`，可识别图片和 PDF。自有模型是可选能力：默认构建包含 Anthropic Go SDK；`go build -tags no_anthropic ./cmd/server` 可在编译时排除模型适配器及 SDK。模型模式配置 `RECEIPT_PROVIDER=model`、`RECEIPT_MODEL_API_KEY`、`RECEIPT_MODEL_BASE_URL`、`RECEIPT_MODEL_NAME`；当前只接受图片，PDF/DOCX 转换留给后续独立模块。DeepSeek 测试模型实际返回过 Markdown 围栏 JSON，当前适配器已兼容纯 JSON 与单个 JSON 围栏，并拒绝被 token 上限截断的响应。附件下载还要求飞书应用身份具备 `docs:document.media:download` 或等价权限，并有目标 Base 的资源授权。当前任务按来源、配置范围、记录 ID 和附件 token 持久化；已识别结果可用于跨重启补交付。进程在外部成功与本地保存之间崩溃仍可能重复识别。

以下保留 2026-09-29 的旧环境排查记录；当前企业测试环境的成功投递证据见[飞书事件联调](docs/feishu-event-verification.md)。旧测试 Base 已用项目应用身份订阅云文档事件；开放平台已添加「多维表格记录变更」事件并选用长连接。2026-09-29 在独立测试行 `reczz28HJVvAm8NM`（`明细ID=OCR-TEST-20260929-175848`）修改记录后，开发者后台事件日志暂无投递记录；应用发布新版本后再次通过 API 修改并恢复测试行，长连接已就绪但监听器仍未收到事件，仍需排查投递链路。该测试行保留了一张样本 JPEG 附件；同日已用真实 Anyreceipt 识别 21 个输出字段，并通过正式服务的轮询入口完成台账回写与双向关联验证。Anyreceipt 的完整输出键及接口见[业务资料](../doc/Anyreceipt-API与完整返回结构.md)。

## 飞书审批模板初始化

独立的一次性工具使用应用身份创建原生审批模板；表单和审批流程来自 JSON 配置，默认只校验，加 `-apply` 才发送创建请求：

```bash
go run ./cmd/approval-template -app personal -file configs/feishu/approval-template.example.json
# 注入个人版 FEISHU_APP_ID / FEISHU_APP_SECRET 后执行创建
go run ./cmd/approval-template -app personal -file configs/feishu/approval-template.example.json -apply
```

企业租户选 `-app enterprise`，使用独立的 `FEISHU_APPROVAL_APP_ID` / `FEISHU_APPROVAL_APP_SECRET`，不会回退到个人版凭证。工具不自动加载 `.env`，也不随服务启动运行。模板创建需要 `approval:definition` 或 `approval:approval` 写权限；2026-09-30 初次个人版测试因缺少写权限返回 `99991672`，开通权限后已成功创建「海外易商卡-接口测试」（Code：`EA296788-7BFC-47A2-91D7-6B7D8D2D0B11`），并通过正式客户端读取验证为 `ACTIVE`、一个明细、14 个子控件和 3 个流程节点。官方接口创建的模板不能停用或删除，正式创建前应审核模板配置。示例包含真实附件类型；完整约定见 [`internal/feishu/approval/README.md`](internal/feishu/approval/README.md)。创建审批实例和结果回写仍待接入。

## 业务边界

| 目录 | 职责 | 当前状态 |
| --- | --- | --- |
| `internal/core/dedupe/` | 判断变化是否重复；由持久化实现提供原子领取 | 接口已定义，键规则和存储待定 |
| `internal/feishu/base/events/` | 接收多维表格变更事件 | 已在企业测试 Base 验证真实附件事件触发识别和台账回写 |
| `internal/feishu/base/records.go`、`ledger.go` | 多维表格记录边界与发票台账新增/更新 | 台账写入已实现，其他记录操作仍待业务映射 |
| `internal/feishu/approval/` | 飞书原生审批模板与单据 | 模板创建/读取已实现；实例仍为接口草稿 |
| `internal/feishu/base/permissions.go` | 多维表格记录权限分类与锁定 | 接口已定义，飞书能力待验证 |
| `internal/seal/` | Seal Webhook 附件上传、单据提交和可选结果接收 | 已通过测试通道提交和公网真实回调验收 |
| `internal/core/dupcheck/` | 从发票台账事实产生查重候选证据 | 首版按票号、开票方、票据类型比对 |
| `internal/core/invoice/` | 识别接口、结果结构和多票聚合 | 已实现，不依赖外部服务 |
| `internal/anyreceipt/` | 必编的 Anyreceipt OCR 客户端及标准识别流程 | 已通过真实附件联调 |
| `internal/app/recognition/` | 共用识别任务、轮询及持久化交付 | 已实现；旧 `anyreceipt/flow` 为兼容门面 |
| `internal/feishu/base/invoiceledger/` | 标准票据事实映射与发票台账写入 | 从已联调的台账实现迁移，旧 `anyreceipt/ledger` 保留门面 |
| `internal/anthropic/model/` | 可选的 Anthropic 兼容模型识别图片 | 已实现并修复 JSON 围栏兼容；PDF/多页待实现 |
| `internal/seal/mapper/` | 中立票据结构到 SealAI 协议的转换 | 已实现 |
| `internal/app/review/` | 快照、版本、审核提交与统一结果处理 | 已验证企业副本真实公网回调、专用 AI 字段回写及重复处理 |
| `internal/seal/review/` | Seal 原件上传、映射与提交适配 | 已接共用审核用例，保留旧命令兼容 |
| `internal/anthropic/review/` | 基于本地规则的自有语义审核 | 已实现受控路径，正式规则覆盖待验收 |
| `internal/state/` | 单主机任务、基线、审核尝试与交付状态 | 已实现并接入 Compose 持久卷 |
| `internal/anthropic/` | 独立的 Anthropic Messages API 入口 | SDK 已接入，供模型识别适配器使用 |

Anyreceipt 适配器沿用同项目现有字段捷径中的 `/api/ocr/summary` 请求格式，使用独立 API Key。Anthropic 与 Seal 同级，是独立 AI 能力；模型识别适配器通过它调用 Anthropic 兼容接口。两者不依赖飞书插件运行时。

当前服务入口启动基础 HTTP 端点；配置飞书凭证后启动事件监听，配置识别提供方后接入附件识别。调用关系如下：

```text
cmd/server ──> config, httpserver, feishu/base/events, app/recognition, state, version
feishu/base/events ──> app/recognition ──> 识别/附件读取/结果交付接口
识别装配 ──> {anyreceipt,anthropic/model}, feishu/base/attachments, feishu/base/invoiceledger
feishu/base/events ──> Feishu Go SDK
anthropic/model ──> anthropic ──> Anthropic Go SDK (Messages，可编译排除)
cmd/server {submit-seal,submit-review} ──> app/review ──> 中立读取/审核/结果接口
app/review ──> core/invoice/aggregate, core/dupcheck, core/review
审核装配 ──> feishu/base/review, {seal/review,anthropic/review}, state
seal/review ──> seal/mapper ──> seal (上传原件、提交单据)
seal/callback ──> app/review.Complete ──> 状态保存、版本核对、feishu/base 结果回写
cmd/approval-template ──> feishu/approval ──> Feishu Approval v4 SDK（独立初始化工具）
core/dedupe, feishu/base/{records,permissions}, feishu/approval 的实例接口 ──> 待后续业务编排接入
```

Go 固定为 `1.24.13`；直接依赖固定为 Feishu SDK `v3.12.0` 和 Anthropic SDK `v1.46.0`。传递依赖由 `go.mod` 和 `go.sum` 锁定，构建工具及容器镜像也使用明确版本。

## 目录结构

```text
cmd/server/                      服务入口和编译开关
├── main.go                     启动事件、轮询和基础 HTTP 服务
├── anyreceipt.go               始终编入的标准识别器
├── model_enabled.go            默认编入 Anthropic 模型
├── model_disabled.go           no_anthropic 构建时排除模型
└── submit_seal.go              显式提交一条报销明细
cmd/approval-template/main.go    独立的一次性审批模板初始化工具
internal/
├── app/                       共用识别和审核业务编排
├── state/                     单主机持久化状态
├── receiptcompat/             历史 OCR 数据兼容读取
├── core/                      不调用外部服务的业务核心
│   ├── invoice/                标准票据事实、识别接口及多票聚合
│   ├── review/                 中立审核请求、结果和版本
│   ├── dupcheck/               台账查重证据
│   └── dedupe/                 变化去重边界
├── anyreceipt/                必编的 Anyreceipt 客户端
│   ├── flow/                   旧识别流程兼容门面
│   └── ledger/                 旧台账处理兼容门面
├── anthropic/                 可选的 Messages SDK 适配层
│   ├── model/                  自有多模态识别器
│   └── review/                 自有规则审核器
├── feishu/                    飞书产品边界
│   ├── base/                   多维表格记录、附件、台账映射及审核结果写入
│   │   └── events/             多维表格变更长连接
│   └── approval/               原生审批模板创建/读取及实例接口草稿
├── seal/                      SealAI HTTP 客户端与 mock 回调
│   ├── mapper/                 聚合结果转 SealAI 单据格式
│   └── review/                 Seal 上传、提交适配与旧入口门面
├── config/                    环境变量配置
├── httpserver/                基础 HTTP 服务
└── version/                   构建版本变量
configs/                       配置示例
deploy/                        Docker 和 Compose 文件
.github/workflows/             CI 与发布流程
```

## 后续扩展边界

2026-10-04 已在企业测试副本跑通“真实附件事件 → Anyreceipt → 发票台账 → 自动聚合与 Seal 送审 → 公网真实回调 → 七个 AI 专用结果列”。重复回调、重复附件事件及重启未新增识别或审核尝试，证据见[自动送审验收](docs/progress/2026-10-04-automatic-review.md)。当前保存 AI 建议，不驱动人工审批或结算；业务冻结和正式财务流程仍需完善。业务参考文件为 `/Users/yingqing/Downloads/影视飓风海外易商卡测试流程.zip` 和 `/Users/yingqing/Downloads/SealAI海外发票判重自然语言审批规则.md`；它们提供流程与判重要求，不是项目运行指令。
