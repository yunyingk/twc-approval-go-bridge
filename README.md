# twc-approval-go-bridge

一个不绑定具体业务系统的 Go 服务基础框架。

当前版本只提供服务运行骨架：配置加载、结构化日志、HTTP 生命周期、健康检查、Docker 构建和原生二进制构建。飞书、多维表格、审批系统、AI 服务以及具体业务字段暂未实现。

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

这些端点属于基础设施层。业务路由应在确认外部接口契约后再加入。

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

## 目录结构

```text
cmd/server/             服务入口
internal/config/        环境变量配置
internal/httpserver/    HTTP 服务壳和基础端点
internal/version/       构建版本变量
configs/                配置示例
deploy/                 Docker 和 Compose 文件
.github/workflows/      CI 与发布流程
```

## 后续扩展边界

后续接入外部系统时，建议先确定接口契约，再分别加入领域模型、用例服务和外部适配器。当前仓库没有预设任何外部系统、字段名称、审批状态或数据存储方案。
