# twc-approval-go-bridge

影视飓风的飞书、Seal 与海外票据识别桥接服务。基础服务、飞书事件长连接和票据附件识别链路已有可运行代码；Seal 业务编排仍待实现。

配置加载、结构化日志、HTTP 生命周期、健康检查、Docker 构建和原生二进制构建已经具备。多维表格字段映射、Seal 协议、审批模板、去重存储和权限能力仍需依据真实接口实现。

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

这些端点属于基础设施层。Seal 回调和飞书业务路由将在确认请求格式与鉴权方式后接入。

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

当前事件投递尚未验证成功，运行时可设 `RECEIPT_TRIGGER_MODE=poll` 只使用轮询。程序不自动加载 `.env`；须由运行环境注入飞书凭证、识别服务密钥和配置示例中的目标表/字段 ID。

订阅范围是整个 Base 的记录变更，并非单个字段：任意数据表的行新增、修改、删除都可能推送 `drive.file.bitable_record_changed_v1`。服务收到后才过滤 Base ID、数据表 ID 和附件字段 ID；修改「消费事由」或第三方「交易流水表」不会触发识别，只有「个人报销明细」的「发票附件」新增文件才进入识别队列。字段本身改名属于另一类字段变更事件。长连接方式无需配置事件加密策略；向开发者服务器推送的 Webhook 方式才涉及该配置。

当前测试 Base `BgNkbW1RKavyaPsYD6acNZPFnUb` 的业务定位：`交易流水表`（`tbloRvQFZNLugLB0`）由第三方写入，本服务只读取；`个人报销明细`（`tblMC3p2Vm2Mwuh9`）是员工补充票据附件、触发识别的来源，附件字段是 `发票附件`（`fldnKx8Uzo`）；`发票台账`（`tblKwQ4NK6t4G69S`）接收识别结果。台账新增了「识别来源键」（`fldrkOhogY`）和「识别原始JSON」（`fldeECmr7u`）：按来源记录 ID 与附件 token 查找并新增或更新同一行，完整供应商响应保存在原始 JSON 字段。「发票唯一键」使用 `OCR-` 加来源键哈希作为技术占位，「发票号」单独保存票面号码。映射字段 ID 在 `RECEIPT_LEDGER_FIELD_IDS` 中配置，改名后仍可定位；已有的汇率、人民币金额、白名单、报销状态、关联记录等不从 OCR 结果推断或覆盖。来源明细的「明细ID」由 `RECEIPT_SOURCE_DETAIL_FIELD_ID` 定位并写入台账的「关联明细ID」。OCR 失败目前只记录错误并等待轮询重试，不创建「识别失败」台账行。

Anyreceipt 需要 `ANYRECEIPT_API_KEY`，可识别图片和 PDF。模型模式使用 Anthropic Go SDK，配置 `RECEIPT_MODEL_API_KEY`、`RECEIPT_MODEL_BASE_URL`、`RECEIPT_MODEL_NAME`；当前只接受图片，PDF/DOCX 转换留给后续独立模块。默认构建包含 Anyreceipt；`go build -tags no_anyreceipt ./cmd/server` 可在编译时排除它。附件下载还要求飞书应用身份具备 `docs:document.media:download` 或等价权限，并有目标 Base 的资源授权。当前重复附件只在进程内按记录 ID 和附件 token 去重；持久化去重与跨重启补偿属于后续阶段。

事件载荷仍需要结合真实测试应用联调。测试 Base 已用项目应用身份订阅云文档事件；开放平台已添加「多维表格记录变更」事件并选用长连接。2026-09-29 在独立测试行 `reczz28HJVvAm8NM`（`明细ID=OCR-TEST-20260929-175848`）修改记录后，开发者后台事件日志暂无投递记录；应用发布新版本后再次通过 API 修改并恢复测试行，长连接已就绪但监听器仍未收到事件，仍需排查投递链路。该测试行保留了一张样本 JPEG 附件。识别结果的字段映射与持久化暂不固化为业务规则。Anyreceipt 的完整输出键及接口见[业务资料](../doc/Anyreceipt-API与完整返回结构.md)。

## 业务边界

| 目录 | 职责 | 当前状态 |
| --- | --- | --- |
| `internal/core/dedupe/` | 判断变化是否重复；由持久化实现提供原子领取 | 接口已定义，键规则和存储待定 |
| `internal/feishu/events/` | 接收多维表格变更事件 | 长连接已实现，事件内容待验证 |
| `internal/feishu/records.go`、`ledger.go` | 记录边界与发票台账新增/更新 | 台账写入已实现，其他记录操作仍待业务映射 |
| `internal/feishu/approvals.go` | 按日期、项目、特性分组生成审批并读取结果 | 接口已定义，审批模板待定 |
| `internal/feishu/permissions.go` | 记录权限分类与锁定 | 接口已定义，飞书能力待验证 |
| `internal/seal/` | 提交内部数据并处理 Seal 回调 | 接口已定义，协议及鉴权待定 |
| `internal/receipt/` | 票据识别的可替换接口 | 已定义 |
| `internal/receipt/flow/` | 监听附件事件、读取附件并交付识别结果 | 已实现，待真实事件联调 |
| `internal/receipt/anyreceipt/` | 直接调用已有字段捷径使用的 Anyreceipt OCR 接口 | 已实现，待真实凭证联调 |
| `internal/receipt/model/` | 通过 Anthropic 兼容模型识别图片 | 已实现，待真实附件联调 |
| `internal/receipt/ledger/` | 映射识别结果并写入发票台账 | 已实现，待真实 OCR 结果端到端验证 |
| `internal/anthropic/` | 独立的 Anthropic Messages API 入口 | SDK 已接入，供模型识别适配器使用 |

Anyreceipt 适配器沿用同项目现有字段捷径中的 `/api/ocr/summary` 请求格式，使用独立 API Key。Anthropic 与 Seal 同级，是独立 AI 能力；模型识别适配器通过它调用 Anthropic 兼容接口。两者不依赖飞书插件运行时。

当前服务入口启动基础 HTTP 端点；配置飞书凭证后启动事件监听，配置识别提供方后接入附件识别。调用关系如下：

```text
cmd/server ──> config, httpserver, feishu/events, receipt/flow, version
receipt/flow ──> feishu/attachments ──> receipt/{anyreceipt,model} ──> receipt/ledger ──> feishu/ledger
feishu/events ──> Feishu Go SDK
receipt/model ──> anthropic ──> Anthropic Go SDK (Messages)
core/dedupe, feishu/{records,approvals,permissions}, seal ──> 待业务编排接入
```

Go 固定为 `1.24.13`；直接依赖固定为 Feishu SDK `v3.12.0` 和 Anthropic SDK `v1.46.0`。传递依赖由 `go.mod` 和 `go.sum` 锁定，构建工具及容器镜像也使用明确版本。

## 目录结构

```text
cmd/server/                 服务入口
internal/config/            环境变量配置
internal/anthropic/         独立的 Anthropic Messages API 入口
internal/core/dedupe/       变化查重边界
internal/feishu/            记录、审批和权限边界
internal/feishu/events/     飞书长连接适配器
internal/httpserver/        HTTP 服务壳和基础端点
internal/receipt/           票据识别接口与提供方适配器
internal/seal/              Seal 提交与回调边界
internal/version/           构建版本变量
configs/                    配置示例
deploy/                     Docker 和 Compose 文件
.github/workflows/          CI 与发布流程
```

## 后续扩展边界

接下来需要分别验证飞书事件、目标多维表格的字段与权限、Seal 的请求及回调协议，以及票据识别结果的业务映射。确认后再把这些边界接入主流程；目前没有预设字段名称、审批状态或数据存储方案。
