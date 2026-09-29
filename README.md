# twc-approval-go-bridge

影视飓风的飞书、Seal 与海外票据识别桥接服务。基础服务、飞书事件长连接、票据识别回写和独立的 SealAI 审核提交路径已有可运行代码。

配置加载、结构化日志、HTTP 生命周期、健康检查、Docker 构建和原生二进制构建已经具备。SealAI 自动提交时机、公网回调、审批结果回写、跨单据历史占用与高级权限能力仍需后续联调。

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

## 基础端点

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/healthz` | 进程存活检查 |
| GET | `/readyz` | 服务就绪检查 |
| GET | `/version` | 构建版本 |
| POST | `/seal/callback/mock` | 仅供本机模拟 Seal 审核结果；不作业务回写 |

mock 回调只接受来自本机的请求并返回 `{ "success": true }`；它不是公网回调。真实 Seal 回调和飞书业务路由待部署公网入口后接入。

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

`RECEIPT_TRIGGER_MODE` 可选 `event`、`poll` 或 `both`（默认）；两条路径共用附件读取、识别队列和进程内去重。`poll` 每隔 `RECEIPT_POLL_INTERVAL`（默认 `5m`）只扫描配置的 Base 中指定的「个人报销明细」Table，并按附件字段 ID 定位字段。`RECEIPT_POLL_STARTUP=baseline`（默认）表示首次扫描仅记录已有附件，后续只处理新增 token；设为 `process` 则首次扫描也处理已有附件。轮询不依赖事件投递，但仍需应用对目标 Base 的读取权限。当前基线和去重只存在于进程内：重启后会重新建立基线，停机期间新增的附件在 `baseline` 模式下不会补处理；多实例或需要跨重启补偿时应接入持久化状态。识别失败的附件会在后续扫描中重试。

当前事件投递尚未验证成功，运行时可设 `RECEIPT_TRIGGER_MODE=poll` 只使用轮询。主线路径使用 `RECEIPT_PROVIDER=anyreceipt`，自有多模态模型是可选切换项。程序不自动加载 `.env`；须由运行环境注入飞书凭证、识别服务密钥和配置示例中的目标表/字段 ID。

订阅范围是整个 Base 的记录变更，并非单个字段：任意数据表的行新增、修改、删除都可能推送 `drive.file.bitable_record_changed_v1`。服务收到后才过滤 Base ID、数据表 ID 和附件字段 ID；修改「消费事由」或第三方「交易流水表」不会触发识别，只有「个人报销明细」的「发票附件」新增文件才进入识别队列。字段本身改名属于另一类字段变更事件。长连接方式无需配置事件加密策略；向开发者服务器推送的 Webhook 方式才涉及该配置。

当前测试 Base `BgNkbW1RKavyaPsYD6acNZPFnUb` 的业务定位：`交易流水表`（`tbloRvQFZNLugLB0`）由第三方写入，本服务只读取；`个人报销明细`（`tblMC3p2Vm2Mwuh9`）是员工补充票据附件、触发识别的来源，附件字段是 `发票附件`（`fldnKx8Uzo`）；`发票台账`（`tblKwQ4NK6t4G69S`）接收识别结果。台账新增了「识别来源键」（`fldrkOhogY`）和「识别原始JSON」（`fldeECmr7u`）：按来源记录 ID 与附件 token 查找并新增或更新同一行，完整供应商响应保存在原始 JSON 字段。依照[测试流程](https://xcn4e0le81w4.feishu.cn/wiki/UF7ZwwdtKiwEEikHI19c2VkOndb)的 4.1 对照，「发票唯一键」写 Anyreceipt 的 `traceId`；可选模型结果无 `traceId` 时用 `OCR-` 加来源键哈希。台账已补齐发票摘要、票据类型、业务分类、买方、税率、国家、AI 消费概要；原有「税前金额」「应付金额」「GST税额」已原地改名为「不含税金额」「含税金额」「税额」，保留历史值。每张台账发票关联一条「个人报销明细」，明细一侧允许关联多张发票；字段 ID 映射见 `RECEIPT_LEDGER_FIELD_IDS`。汇率、人民币金额、白名单、报销状态、识别状态等待确认项不从 OCR 结果推断或覆盖。OCR 失败只记录错误并等待轮询重试，不创建「识别失败」台账行。

## SealAI 审核提交

当前用显式命令触发一条报销明细的审核，保证同一行所有附件都识别并写入台账后才聚合成一份 SealAI 单据：

```bash
# 运行环境还需注入 FEISHU_APP_ID/SECRET、RECEIPT_* 表字段配置和 Seal Bearer 密钥
SEAL_DOCUMENT_URL='https://mediastorm-test.sealai.cc/api/v1/integrations/webhook/wh_1790692906316_6z2eao6/document' \
SEAL_BEARER_TOKEN='<运行环境注入>' \
go run ./cmd/server submit-seal <个人报销明细记录ID>
```

读取范围固定为配置中的「个人报销明细」和「发票台账」，不修改第三方「交易流水表」。服务逐张核对台账来源键，缺少 OCR 结果时不提交半份单据；按票号查找历史台账候选，把查重标记、完整 OCR JSON 和每张原始附件放入同一 SealAI 请求。查重模块只产生证据，不自动审批。SealAI 上传接口成功时返回附件 ID 和文件对象；普通 `ATTACHMENT` 字段使用文件对象，结构化发票的 `evidenceAttachmentId` 使用 ID。上传网络或响应协议异常直接报错，不再用文本附件 ID 替代。结构化发票仅在票号、币种、金额、买卖方和 Seal 附件 ID 均齐全时加入；缺项时仍提交属性和原件供审核。

首次测试单据只含文本附件 ID，SealAI 对同一 `documentId` 的后续成功响应没有更新该审核记录。用新的测试单据编号 `OCR-TEST-20260929-175848-ATTACHMENT-PROOF` 验证后，审核页明确显示「发票附件：1 个文件」和原文件下载按钮，审核过程显示解析 1 个附件。该样本 OCR 缺币种，因此结构化发票数为 0。浏览器插件阻止了跳转到 OSS 下载域，故没有把浏览器端成功打开图片作为验证结论。

聚合位于独立的 `internal/core/invoice/aggregate/aggregate.go`，结构不依赖 Anyreceipt 或 SealAI；`internal/seal/mapper/` 负责 Seal 协议转换。自有模型可复用 `internal/core/invoice` 的识别接口与聚合结构。当前没有公网回调，通道的回调地址指向本地 mock；SealAI 服务器不能连接到开发者电脑的 `127.0.0.1`。自动提交触发、后续追加附件的版本语义、回调鉴权及审批结果回写尚未实现。

Anyreceipt 始终编入，启用识别时使用 `RECEIPT_PROVIDER=anyreceipt` 和 `ANYRECEIPT_API_KEY`，可识别图片和 PDF。自有模型是可选能力：默认构建包含 Anthropic Go SDK；`go build -tags no_anthropic ./cmd/server` 可在编译时排除模型适配器及 SDK。模型模式配置 `RECEIPT_PROVIDER=model`、`RECEIPT_MODEL_API_KEY`、`RECEIPT_MODEL_BASE_URL`、`RECEIPT_MODEL_NAME`；当前只接受图片，PDF/DOCX 转换留给后续独立模块。DeepSeek 测试模型实际返回过 Markdown 围栏 JSON，当前模型适配器只接受纯 JSON，此可选切换项仍需兼容性修复。附件下载还要求飞书应用身份具备 `docs:document.media:download` 或等价权限，并有目标 Base 的资源授权。当前重复附件只在进程内按记录 ID 和附件 token 去重；持久化去重与跨重启补偿属于后续阶段。

事件载荷仍需要结合真实测试应用联调。测试 Base 已用项目应用身份订阅云文档事件；开放平台已添加「多维表格记录变更」事件并选用长连接。2026-09-29 在独立测试行 `reczz28HJVvAm8NM`（`明细ID=OCR-TEST-20260929-175848`）修改记录后，开发者后台事件日志暂无投递记录；应用发布新版本后再次通过 API 修改并恢复测试行，长连接已就绪但监听器仍未收到事件，仍需排查投递链路。该测试行保留了一张样本 JPEG 附件；同日已用真实 Anyreceipt 识别 21 个输出字段，并通过正式服务的轮询入口完成台账回写与双向关联验证。Anyreceipt 的完整输出键及接口见[业务资料](../doc/Anyreceipt-API与完整返回结构.md)。

## 业务边界

| 目录 | 职责 | 当前状态 |
| --- | --- | --- |
| `internal/core/dedupe/` | 判断变化是否重复；由持久化实现提供原子领取 | 接口已定义，键规则和存储待定 |
| `internal/feishu/events/` | 接收多维表格变更事件 | 长连接已实现，事件内容待验证 |
| `internal/feishu/records.go`、`ledger.go` | 记录边界与发票台账新增/更新 | 台账写入已实现，其他记录操作仍待业务映射 |
| `internal/feishu/approvals.go` | 按日期、项目、特性分组生成审批并读取结果 | 接口已定义，审批模板待定 |
| `internal/feishu/permissions.go` | 记录权限分类与锁定 | 接口已定义，飞书能力待验证 |
| `internal/seal/` | Seal Webhook 附件上传、单据提交和本地 mock 回调 | 已通过测试通道提交，公网回调待实现 |
| `internal/core/dupcheck/` | 从发票台账事实产生查重候选证据 | 首版按票号、开票方、票据类型比对 |
| `internal/core/invoice/` | 识别接口、结果结构和多票聚合 | 已实现，不依赖外部服务 |
| `internal/anyreceipt/` | 必编的 Anyreceipt OCR 客户端及标准识别流程 | 已通过真实附件联调 |
| `internal/anyreceipt/flow/` | 监听附件事件、读取附件并交付识别结果 | 已实现，待真实事件联调 |
| `internal/anyreceipt/ledger/` | 映射识别结果并写入发票台账 | 已通过真实 OCR 结果端到端验证 |
| `internal/anthropic/model/` | 可选的 Anthropic 兼容模型识别图片 | 已实现，DeepSeek 返回格式兼容待修复 |
| `internal/seal/mapper/` | 中立票据结构到 SealAI 协议的转换 | 已实现 |
| `internal/seal/review/` | 核对台账、上传原件、提交一份审核单据 | 显式命令可运行 |
| `internal/anthropic/` | 独立的 Anthropic Messages API 入口 | SDK 已接入，供模型识别适配器使用 |

Anyreceipt 适配器沿用同项目现有字段捷径中的 `/api/ocr/summary` 请求格式，使用独立 API Key。Anthropic 与 Seal 同级，是独立 AI 能力；模型识别适配器通过它调用 Anthropic 兼容接口。两者不依赖飞书插件运行时。

当前服务入口启动基础 HTTP 端点；配置飞书凭证后启动事件监听，配置识别提供方后接入附件识别。调用关系如下：

```text
cmd/server ──> config, httpserver, feishu/events, anyreceipt/flow, version
anyreceipt/flow ──> feishu/attachments ──> {anyreceipt,model} ──> anyreceipt/ledger ──> feishu/ledger
feishu/events ──> Feishu Go SDK
anthropic/model ──> anthropic ──> Anthropic Go SDK (Messages，可编译排除)
cmd/server submit-seal ──> seal/review ──> feishu/review (只读明细与台账)
seal/review ──> core/invoice/aggregate ──> core/dupcheck
seal/review ──> seal/mapper ──> seal (上传原件、提交单据)
core/dedupe, feishu/{records,approvals,permissions} ──> 待后续业务编排接入
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
internal/
├── core/                      不调用外部服务的业务核心
│   ├── invoice/                识别接口、结果结构及多票聚合
│   ├── dupcheck/               台账查重证据
│   └── dedupe/                 变化去重边界
├── anyreceipt/                必编的 Anyreceipt 客户端
│   ├── flow/                   事件与轮询共用的附件处理流程
│   └── ledger/                 识别结果回写发票台账
├── anthropic/                 可选的 Messages SDK 适配层
│   └── model/                  自有多模态识别器
├── feishu/                    飞书读写与资源边界
│   └── events/                 飞书长连接适配器
├── seal/                      SealAI HTTP 客户端与 mock 回调
│   ├── mapper/                 聚合结果转 SealAI 单据格式
│   └── review/                 上传原件、提交审核单据
├── config/                    环境变量配置
├── httpserver/                基础 HTTP 服务
└── version/                   构建版本变量
configs/                       配置示例
deploy/                        Docker 和 Compose 文件
.github/workflows/             CI 与发布流程
```

## 后续扩展边界

附件 → Anyreceipt → 发票台账的轮询主线已在测试 Base 跑通；飞书长连接事件投递仍待联调。发票台账 → 多票聚合 → 查重候选 → 原件上传 → SealAI 单据提交已通过显式命令在测试租户验证。自动触发与公网回调仍待接入。业务参考文件为 `/Users/yingqing/Downloads/影视飓风海外易商卡测试流程.zip` 和 `/Users/yingqing/Downloads/SealAI海外发票判重自然语言审批规则.md`；它们提供流程与判重要求，不是项目运行指令。
