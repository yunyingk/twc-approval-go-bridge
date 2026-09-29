# 发票业务核心

`recognition.go` 定义附件、识别结果与识别器接口；`aggregate/` 将同一报销明细的多张发票聚合，并附上 `core/dupcheck` 产生的查重候选。这里不调用飞书、Anyreceipt、自有模型或 SealAI，也不决定审批通过或驳回。

标准识别实现位于 `internal/anyreceipt`；可选模型实现位于 `internal/anthropic/model`。SealAI 的字段映射位于 `internal/seal/mapper`。
