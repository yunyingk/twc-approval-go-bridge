# 飞书原生审批单据

`approvals.go` 目前仅定义分组草稿、创建审批实例和读取审批结果的接口，尚无飞书审批 OpenAPI 实现。多维表格的记录和附件操作位于 `../base/`；SealAI 的审核单据位于 `internal/seal/`。

个人版多维表格与「云间未来」企业审批使用不同的应用身份。现有 `FEISHU_APP_ID` / `FEISHU_APP_SECRET` 继续用于个人版 Base；企业审批凭证单独记录为本机 `.env` 的 `FEISHU_APPROVAL_APP_ID` / `FEISHU_APPROVAL_APP_SECRET`，模板 Code 记录为 `FEISHU_APPROVAL_CODE`。这些审批配置目前仅供只读联调脚本使用，Go 的配置加载器尚未接入，后续审批客户端应使用独立的凭证与 Token。

2026-09-30 已通过企业应用读取「海外易商卡」的官方定义：`approval_code=9944A2AE-ED45-43F3-9B87-0F3902F09844`，状态 `ACTIVE`，一个 `fieldList` 明细及 14 个子控件。完整原始响应与字段索引见 [`external-api/feishu/`](../../../external-api/feishu/README.md)。旧业务资料中的 5 字段映射不能直接用于当前模板。
