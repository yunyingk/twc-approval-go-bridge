# SealAI 审核编排

2026-10-03 起，共用编排迁至 `internal/app/review`；本目录主要实现 Seal `Gateway`，旧编排包装已于 2026-10-08 移除。新入口是 `submit-review`，支持版本、持久化状态与统一结果交付。Seal 的规则仍在 SealAI 系统维护。下文保留此前的联调说明，当前边界见 [运行与交接说明](../../../docs/runtime-and-handoff.md)。

Gateway 接收共用用例准备好的 core.Request，上传每份原件并通过映射适配器提交 SealAI 单据。完整读取与中立聚合由 app/review 完成；本目录只依赖 SealGateway，不读取飞书、不维护业务编排。缺票时共用用例阻止提交部分单据，查重证据不自动批准或驳回。

以下为首版联调历史，submit-seal 已移除、公网回调已验收。

首版通过 `go run ./cmd/server submit-seal <个人报销明细记录ID>` 显式触发审核，便于联调同一行多票的完整性。自动提交时机、后续新增附件的版本语义、Seal 回调公网入口与审批结果回写在此基础上单独增加。当前无公网回调，可用本地 mock 验证入站结构。
