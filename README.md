# twc-approval-go-bridge

影视飓风的飞书、Seal 与海外票据识别桥接服务。仓库已从通用服务骨架进入业务架构阶段；目前只有基础服务、飞书事件长连接和 Anyreceipt 适配器具备可运行代码。

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

当前监听器只负责建立连接、注册事件类型并接收原始事件。默认日志记录事件 ID、事件类型和载荷大小，不解析具体字段，也不执行附件判断、队列处理或业务回写。调试原始载荷时可以临时设置 `FEISHU_LOG_RAW_EVENTS=true`。

事件类型和事件载荷需要结合真实测试应用继续验证。豆包资料中提到的字段结构不会在验证前固化为业务规则。

## 业务边界

| 目录 | 职责 | 当前状态 |
| --- | --- | --- |
| `internal/core/dedupe/` | 判断变化是否重复；由持久化实现提供原子领取 | 接口已定义，键规则和存储待定 |
| `internal/feishu/events/` | 接收多维表格变更事件 | 长连接已实现，事件内容待验证 |
| `internal/feishu/records.go` | 读取和回写多维表格记录 | 接口已定义，字段映射待定 |
| `internal/feishu/approvals.go` | 按日期、项目、特性分组生成审批并读取结果 | 接口已定义，审批模板待定 |
| `internal/feishu/permissions.go` | 记录权限分类与锁定 | 接口已定义，飞书能力待验证 |
| `internal/seal/` | 提交内部数据并处理 Seal 回调 | 接口已定义，协议及鉴权待定 |
| `internal/receipt/` | 票据识别的可替换接口 | 已定义 |
| `internal/receipt/anyreceipt/` | 直接调用已有字段捷径使用的 Anyreceipt OCR 接口 | 已实现，待真实凭证联调 |
| `internal/receipt/anthropic/` | 备用的 Anthropic Messages API 入口 | SDK 已接入，识别映射待定 |

Anyreceipt 适配器沿用同项目现有字段捷径中的 `/api/ocr/summary` 请求格式，使用独立 API Key。Anthropic 适配器目前只封装标准 Messages API，不设定模型、提示词或图片传输方式，因此还没有接入 `receipt.Recognizer`。两者不依赖飞书插件运行时。

当前服务入口只启动飞书监听和基础 HTTP 端点。各业务接口尚未接入主流程，调用关系如下：

```text
cmd/server ──> config, httpserver, feishu/events, version
feishu/events ──> Feishu Go SDK
receipt/anyreceipt ──> receipt.Recognizer
receipt/anthropic ──> Anthropic Go SDK (Messages)
core/dedupe, feishu/{records,approvals,permissions}, seal ──> 待业务编排接入
```

Go 固定为 `1.24.13`；直接依赖固定为 Feishu SDK `v3.12.0` 和 Anthropic SDK `v1.46.0`。传递依赖由 `go.mod` 和 `go.sum` 锁定，构建工具及容器镜像也使用明确版本。

## 目录结构

```text
cmd/server/                 服务入口
internal/config/            环境变量配置
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
