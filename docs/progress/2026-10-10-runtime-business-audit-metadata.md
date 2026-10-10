# 2026-10-10 引入运行时业务元数据与操作审计追溯体系

## 1. 业务背景与诉求

在海外小票识别与机审自动化主线中，业务和财务需要清晰地查询关键生命周期节点的元数据事实：
1. **什么时候推送了谁**：卡片催报通知是在何时发给哪位持卡人的？
2. **谁填了文件**：发票附件是由谁何时上传填报的？
3. **什么时候发起了 OCR**：小票是什么时候送去 Anyreceipt 进行识别的，识别出了什么？
4. **什么时候发起了审核**：单据是在何时送审 SealAI 的，机审结论与回写状态是什么？

---

## 2. 实施架构与技术设计

### 2.1 结构化审计模型 (`internal/audit/entry.go`)
定义标准事件类型与元数据实体：
- `notify_card_sent`：推送补票卡片（记录持卡人 open_id、姓名、交易流水 ID、金额、商户、消息 ID）
- `file_uploaded`：发票文件上传（记录明细记录 ID、附件 Token、文件名）
- `ocr_initiated` / `ocr_completed` / `ocr_failed`：OCR 任务全生命周期与要素事实
- `review_submitted` / `review_completed` / `review_writeback`：单据送审、机审完成及结果回写

### 2.2 持久化存储与高效检索 (`internal/audit/store.go`)
- 存储于 `cfg.Runtime.StateDir/audit/audit.jsonl`，采用读写锁与系统级 `syscall.Flock` 保护多进程并发安全写入；
- 支持 `Timeline(recordID)`：以单据为主键，检索其从催报、上传、OCR 到机审回写的完整生命周期演进（正序时间线）；
- 支持 `Query(filter)`：支持按人员 (`Operator`)、事件类型 (`Type`)、时间范围 (`Since/Until`)、单据 ID 逆序查询。

### 2.3 关键业务链路埋点
- **补票卡片**：在 `internal/feishu/card/service.go` 的 `NotifyTransaction` 发送成功后自动记录；
- **文件上传**：在 `internal/feishu/base/events/attachments.go` 监听到 Bitable 附件变更及 `internal/app/recognition/flow.go` 抓取文件时自动记录；
- **OCR 识别**：在 `internal/app/recognition/flow.go` 发起识别与识别成功后自动记录要素与耗时；
- **单据机审**：在 `internal/app/review/review.go` 的 `Submit`、`Complete` 和 `deliver` 阶段分别记录送审、机审结论及明细表回写。

### 2.4 查询方式落地
1. **CLI 命令**：
   - `twc-approval-go-bridge audit <record-id> [-json]`：打印单据生命周期树形 ASCII 时间线；
   - `twc-approval-go-bridge audit-query [options]`：多维度复合过滤（支持 `-type`, `-user`, `-id`, `-since`, `-limit`, `-json`）。
2. **HTTP API 与 Web 监控看板**：
   - `GET /api/audit` 与 `GET /api/audit/timeline`；
   - Web Dashboard 集成「业务运行时操作审计流水」实时卡片，并支持输入单据 ID 一键查时间线。

---

## 3. 验收与双构建测试

1. 单元测试 `internal/audit/store_test.go` 与 `cmd/server/audit_test.go` 全部通过；
2. 执行全量双构建与代码检查：
   ```bash
   go test ./...
   go test -tags no_anthropic ./...
   go vet ./...
   ```
   全绿通过无任何错误。
3. 编译二进制并实测 `audit` 与 `audit-query` 输出，格式严谨清晰。
