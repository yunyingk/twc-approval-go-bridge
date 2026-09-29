# Anyreceipt 标准识别流程

`client.go` 调用 Anyreceipt OCR 接口，始终参与编译；`flow/` 接收飞书事件或轮询发现的新增附件并调用 `core/invoice.Recognizer`；`ledger/` 将识别结果映射并回写飞书发票台账。自有模型只是在运行时替换识别器实现，其 SDK 可用 `no_anthropic` 编译标记排除。

多票聚合及查重规则在 `internal/core`，SealAI 映射和审核提交在 `internal/seal`。本目录不包含 SealAI 请求格式或审批决定。
