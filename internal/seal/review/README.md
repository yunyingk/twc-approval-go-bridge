# SealAI 审核编排

2026-10-03 起，共用编排迁至 `internal/app/review`；本目录主要实现 Seal `Gateway`，旧编排包装已于 2026-10-08 移除。新入口是 `submit-review`，支持版本、持久化状态与统一结果交付。Seal 的规则仍在 SealAI 系统维护。下文保留此前的联调说明，当前边界见 [运行与交接说明](../../../docs/runtime-and-handoff.md)。

这里把一条报销明细的全部附件、台账 OCR 结果与查重候选读齐，调用中立聚合模块，然后上传每份原件并通过映射适配器提交一份 SealAI 单据。只依赖 `Source` 和 `SealGateway` 接口；飞书 API 由 `internal/feishu` 实现，Seal HTTP 由 `internal/seal` 实现。不会在缺票的情况下提交部分单据，也不会根据本地查重结果自动批准或驳回。

首版通过 `go run ./cmd/server submit-seal <个人报销明细记录ID>` 显式触发审核，便于联调同一行多票的完整性。自动提交时机、后续新增附件的版本语义、Seal 回调公网入口与审批结果回写在此基础上单独增加。当前无公网回调，可用本地 mock 验证入站结构。
