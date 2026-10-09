# 多维表格一键初始化 (init-bitable) 与反向配置导出实现

2026-10-09，Asia/Shanghai。针对新企业冷启动（仅有企业 Host 与自建应用凭证，无预置 Base 和数据表）的业务痛点，正式实现多维表格声明式一键初始化、高级权限启停、协作者委派与反向导出 `configs/tables/*.json` 配置能力。

## 背景与痛点

过去接入新企业时，需要人工在飞书后台手动创建多维表格容器、手动配置三张数据表与数十个不同类型字段，并逐个抄录 `base_token`、`table_id` 与 `field_id` 拼装配置，费时费力且极易出错。

## 关键技术发现与实施选择

1. **增值版与基础版权限行为实测**：
   - 真实调用测试租户 OpenAPI 探测角色创建接口：飞书官方明确返回 HTTP 403，错误码 `1254304` (`Only Available For Business and Enterprise Editions`)；
   - 验证结论：飞书免费版/基础版拦截细粒度自定义角色创建，但支持标准表权限及高级权限基础模式（`advperm/enable` 实测成功返回 `code: 0`）；
   - 策略选择：在高级角色配置时实施**优雅降级（Graceful Degradation）**，遇 `1254304` 输出友好提示并跳过自定义角色，保障冷启动全流程 100% 顺畅完成，绝不阻塞流程。

2. **模块职责正交划分 (`internal/feishu/provision`)**：
   - `internal/feishu/base`：专注**运行期（Runtime）**流水读取、机审回写、发票台账交付；
   - `internal/feishu/provision`：专注**开通期（Provisioning）**资源生命周期编排与权限管理；
   - `blueprint.go`：定义交易流水表、个人报销明细表、发票台账表 20 个标准列契约与 4 条跨表双向关联；
   - `client.go`：底层封装飞书 Bitable v1 与 Base v3 创建、字段追加、删除默认表、高级权限与成员管理接口；
   - `service.go`：端到端业务编排，反向生成规范化的 `configs/tables/*.json`。

3. **原生 CLI 命令内置与拓扑自验闭环**：
   - `./bin/twc-approval-go-bridge init-bitable`：
     - 支持 `--name`、`--output`、`--profile`、`--folder-token`、`--admin-user`、`--transfer-owner` 等参数；
     - 自动创建 Base、3 张表、字段并配置双向关联；
     - 自动反向生成目标 JSON 配置文件，并就地调用 `checkBusiness` 执行 100% 拓扑自验闭环；
   - `./bin/twc-approval-go-bridge add-admin <base-token> <user-identity>`：快速为财务授权 `full_access`；
   - `./bin/twc-approval-go-bridge transfer-owner <base-token> <user-identity>`：支持将所有者转移给具体财务人员。

4. **健康诊断联动 (`internal/health`)**：
   - 当 `tables_file` 尚未配置或多维表格未初始化时，`doctor` 不再静默跳过，而是给出明确预警与一键初始化指引：
     `请执行 ./bin/twc-approval-go-bridge init-bitable 一键新建并自动生成表配置`。

## 验收

- `internal/feishu/provision` 单元测试与端到端 Mock 验收通过（含基础版 `1254304` 降级用例）；
- `cmd/server/init_bitable_test.go` 命令测试通过；
- `internal/health` 测试通过；
- 双构建验证 `go test ./...` 与 `go test -tags no_anthropic ./...` 100% 通过；
- `go vet ./...` 全量检查通过；
- 命令行二进制 `twc-approval-go-bridge init-bitable -h` 验证输出完整清晰。
