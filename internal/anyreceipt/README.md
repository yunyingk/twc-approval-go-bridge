# Anyreceipt 标准识别流程

`client.go` 调用 Anyreceipt OCR 接口，始终参与编译；`flow/` 接收飞书事件或轮询发现的新增附件并调用 `core/invoice.Recognizer`；`ledger/` 将识别结果映射并回写飞书发票台账。自有模型只是在运行时替换识别器实现，其 SDK 可用 `no_anthropic` 编译标记排除。

供应商原版 OpenAPI YAML 备份见 [`external-api/anyreceipt/`](../../external-api/anyreceipt/anyreceipt-openapi-v1.0.7.yaml)，来源及校验值见 [`external-api/README.md`](../../external-api/README.md)。

多票聚合及查重规则在 `internal/core`，SealAI 映射和审核提交在 `internal/seal`。本目录不包含 SealAI 请求格式或审批决定。
