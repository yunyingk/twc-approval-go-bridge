# Seal AI 票据映射

`Map` 使用调用方提供的 `documentId` 和 `documentSN` 标识一条报销明细；同一明细的多张附件共用这两个值。结构化发票的 `sourceInvoiceId` 为 `feishu-invoice-<detailId>`：`detailId` 优先使用台账记录 ID，缺失时依次取票据事实记录 ID、明细记录 ID 与附件 token 组成的来源键。调用方先上传原附件，再传入上传响应。上传响应必须带完整附件元数据，文档才会加入真正的 `ATTACHMENT` 字段；缺元数据就停止提交，不再把附件 ID 当作文本替代。文档只包含已识别的事实；只有票号、币种、含税金额、买卖方与 Seal 附件 ID 均齐全时，才附结构化发票。当前映射允许购买方缺失时使用唯一报销人姓名；其他必填事实仍不编造。

这个包只负责纯数据映射。飞书记录读取、附件上传、HTTP 提交和回调处理由各自适配器及上层编排负责。
## 多票聚合

`MapBatch` 接收 `internal/core/invoice/aggregate` 的中立单据，将多票映射为 `expense_details` 二维明细（`type=LIST`、`semanticType=EXPENSE_DETAIL_LIST`），每行保存 `detailId` 与台账事实；不再按票序号平铺字段。结构化发票同时带 `claimedAmount` 和按 `detailId` 关联的 `expenseRefs`，查重候选作为审核证据字段提交。原件必须先上传 SealAI，上传结果按附件 token 传入。缺少结构化发票必填事实时仍保留 OCR 字段和原件，不编造币种或金额。
