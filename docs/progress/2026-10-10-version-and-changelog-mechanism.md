# 2026-10-10 引入 0.3.0 版本机制与变更日志追踪体系

## 1. 背景与目标

随着海外易商卡报销网桥逐步接近正式上线节奏，系统需要建立规范、稳定、可追溯的工程化版本与变更追踪体系：
1. **确立基线版本为 `0.3.0`**：结束以往未规范版本标识的阶段，确立符合语义化版本规范（SemVer 2.0.0）的上线基线；
2. **构建元数据自动注入**：编译期自动捕获并注入 Git Commit SHA、UTC 构建时间戳、Go 版本和目标系统架构，使任何运行环境和二进制副本皆可精确定位源码版本；
3. **引入变更日志机制 (CHANGELOG)**：建立 `CHANGELOG.md`，以 Keep a Changelog 格式记录每一个修改的分类（Added / Changed / Fixed / Security 等），并在项目协作规则 `AGENTS.md` 中固化“每一次修改有迹可循”的制度约束。

---

## 2. 实施方案与技术设计

### 2.1 版本包体系 (`internal/version`)
- 将 `internal/version/version.go` 从原先默认 `"dev"` 升级为 `0.3.0` 基线版本；
- 定义构建元数据变量：`GitCommit`、`BuildDate`、`GoVersion`、`Platform`；
- 提供 `Info` 结构体，支持 `Get()`、`Short()`、`Full()` 与 `JSON()` 方法，既向下兼容原始 `version.Version` 变量，又为上层应用提供丰富的版本自省能力；
- 编写完善的单元测试 `internal/version/version_test.go`。

### 2.2 CLI 命令与自检入口 (`cmd/server/main.go`)
- 在命令行解析前端支持 `version`、`-v`、`--version` 快速响应，无需依赖或等待 `configs/config.toml` 加载，遵循标准 Unix CLI 规范；
- 支持 `-json` 参数输出格式化 JSON，便于自动化探测和运维脚本解析；
- 更新帮助手册 `printUsage`，加入版本命令指引。

### 2.3 运行时服务与监控看板联动 (`internal/httpserver`)
- HTTP 基础端点 `GET /version` 返回结构化版本对象；
- 服务启动日志中记录完整版本元数据（`version`, `commit`, `build_date`）；
- Web 监控看板与 `GET /api/status` 实时显示版本号及对应的 Git Commit 标识。

### 2.4 构建工具链收敛
- **Makefile**：默认 `VERSION ?= 0.3.0`，自动获取 `git rev-parse --short HEAD` 与 `date -u +'%Y-%m-%dT%H:%M:%SZ'` 并注入 `-ldflags`；
- **deploy/Dockerfile**：参数化 `ARG VERSION=0.3.0`、`ARG GIT_COMMIT` 与 `ARG BUILD_DATE`；
- **.goreleaser.yaml**：在发布编译 flags 中注入 `Version`、`Commit` 与 `Date`。

### 2.5 变更日志机制 (`CHANGELOG.md` & `AGENTS.md`)
- 根目录建立 `CHANGELOG.md`，完整归纳准上线基线版本 `[0.3.0] - 2026-10-10` 的全部特性：
  - 双 OCR 引擎（Anyreceipt + Anthropic兜底）海外小票识别
  - 飞书 WebSocket 长连接秒级同步与去重防抖
  - 飞书发票台账自动落库与明细表双向关联
  - SealAI 与自定义规则 AI 审核及结果回写
  - 信用卡补票卡片实时推送与批量扫描
  - 单一主配置架构收敛与原生审批封存
  - 运维自检诊断与 Web 监控看板
- 预留 `[Unreleased]` 跟踪区域；
- 在 `AGENTS.md` 增加第 4 节，明确要求每一次修改必须在 `CHANGELOG.md` 中同步登记，让每一个修改有迹可循。

---

## 3. 验收验证

### 3.1 单元测试与 Vet 验证
执行受影响模块测试及全量测试：
```bash
go test -v ./internal/version/...
go test -v ./internal/httpserver/...
go test ./...
go test -tags no_anthropic ./...
go vet ./...
```
全部 100% 通过无任何错误。

### 3.2 编译与命令行输出验证
通过 `make build` 编译出二进制：
- `./bin/twc-approval-go-bridge version` 输出：
  `twc-approval-go-bridge v0.3.0 (commit: 30850c5, built: 2026-10-10T09:25:38Z, go: go1.24.13, darwin/arm64)`
- `./bin/twc-approval-go-bridge -v` 输出一致。
- `./bin/twc-approval-go-bridge version -json` 输出：
  ```json
  {
    "version": "0.3.0",
    "git_commit": "30850c5",
    "build_date": "2026-10-10T09:25:38Z",
    "go_version": "go1.24.13",
    "platform": "darwin/arm64"
  }
  ```
- 无构建注入直接 `go run ./cmd/server version` 输出：
  `twc-approval-go-bridge v0.3.0 (go: go1.24.13, darwin/arm64)`
- 默认基线版本稳定为 `0.3.0`。
