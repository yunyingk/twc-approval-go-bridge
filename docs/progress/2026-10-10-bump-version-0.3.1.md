# 2026-10-10 提升版本至 0.3.1 并为 SealAI 聚焦做准备

## 1. 版本发布说明

根据上线节奏与业务功能迭代：
- 将系统准上线基线版本从 `0.3.0` 升级至 **`0.3.1`**；
- 将上一阶段完成的**运行时业务元数据与操作审计追溯体系 (`internal/audit`)** 正式封版为 `0.3.1`；
- 更新 `CHANGELOG.md`，并在 `[Unreleased]` 中开辟新的跟踪段落，准备下一阶段聚焦 SealAI 审批主线。

---

## 2. 变更清单

1. `internal/version/version.go`：默认版本提升至 `0.3.1`；
2. `internal/version/version_test.go` 与 `internal/httpserver/server_test.go`：同步更新测试断言；
3. `Makefile` 与 `deploy/Dockerfile`：默认构建版本升级为 `0.3.1`；
4. `README.md` 与 `AGENTS.md`：更新当前语义化基线版本说明；
5. `CHANGELOG.md`：正式归档 `[0.3.1] - 2026-10-10`，并重置 `[Unreleased]`。

---

## 3. 下一步聚焦：SealAI

下一个迭代阶段我们将全方位聚焦 SealAI 企业级审批大模型链路：
- SealAI Webhook 发送、凭证校验与 payload 精细化适配；
- SealAI 异步回调与公网入口映射验证；
- 审核规则覆盖度、机审原因及七个专用 AI 字段回写飞书的完整性。
