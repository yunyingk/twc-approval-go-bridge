# 发票台账核心键正名：识别流水号 (trace_id) 与 附件标识 (attachment_key)

2026-10-09，Asia/Shanghai。针对发票台账中历史遗留的两个命名别扭、语义倒挂的核心字段进行彻底正名与语义重构，确立清晰纯粹的命名契约。

## 背景与痛点

1. **`bridge_unique_key`（发票唯一键）**：
   - 实际写入的是外部 OCR 供应商（如 Anyreceipt）单次调用的请求追踪流水号（`traceId`）；
   - 叫“发票唯一键”极易误导业务和财务人员，误以为是发票税号或物理实体防重指纹。
2. **`bridge_source_key`（识别来源键）**：
   - 实际是系统内部由 `明细RecordID:文件Token` 拼接而成、用于在 OCR 调用前实现幂等查重与更新的底层主键；
   - 叫“识别来源键”名称过长、术语抽象，未直观体现其“锚定具体明细附件”的本质。

## 正名决策与改动

经与业务对齐，在未上线阶段彻底推翻别扭的旧命名，确立新命名：

| 原配置 Key | 原列名 | 新配置 Key | 新列名 | 真实角色 |
| :--- | :--- | :--- | :--- | :--- |
| `bridge_unique_key` | 发票唯一键 | **`trace_id`** | **识别流水号** | 外部 OCR 供应商返回的调用追踪流水号 (`traceId`) |
| `bridge_source_key` | 识别来源键 | **`attachment_key`** | **附件标识** | 明细附件在系统底层的唯一指针 (`RecordID:FileToken`) |

## 实施范围

1. **配置层**：
   - 更新 `configs/tables/enterprise.example.json` 及测试配置：将 `bridge_unique_key` 升级为 `trace_id`（识别流水号），将 `bridge_source_key` 升级为 `attachment_key`（附件标识）。
2. **校验与适配层**：
   - `internal/config/business.go` 与 `internal/config/config.go`：优先以 `attachment_key` 进行必填校验；
   - `internal/feishu/base/invoiceledger/ledger.go`：正式引入 `AttachmentKey` 与 `TraceID` 常量，`New` 与 `Handle` 优先写入和按新键索引；
   - `internal/feishu/base/review.go` 与 `cmd/server/review_source_events.go`：送审读取与事件监听同步支持新语义。
3. **测试用例**：
   - `ledger_test.go` 新增 `TestHandleMapsAttachmentKeyAndTraceID` 专项单测。

## 验证与验收

- `go test ./...` 100% 通过；
- `go test -tags no_anthropic ./...` 100% 通过；
- `go vet ./...` 0 警告 0 错误；
- 二进制构建通过。
