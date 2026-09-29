# Seal AI Webhook 客户端

本目录只封装 Seal AI Webhook 的出站 HTTP 协议；不读取飞书表格，不处理 OCR，也不暴露回调路由。业务映射在调用方完成。

使用 `NewClient(Config{DocumentURL, BearerToken}, httpClient)` 创建客户端。`DocumentURL` 是目标通道的完整 `/api/v1/integrations/webhook/{webhookId}/document` 地址；附件地址由同一通道推导为 `/attachments`。Bearer 密钥由运行环境注入，不写入仓库。默认请求超时为 60 秒；传入有 `Timeout` 的 `http.Client` 可覆盖。客户端不跟随重定向，以免向另一地址转发密钥。

调用顺序：

1. `UploadAttachment(ctx, Attachment{Name, ContentType, Data})` 以 multipart 字段 `files` 上传已校验的原始附件，取得 `AttachmentID` 与服务器返回的附件元数据。文件类型白名单和真实内容识别由调用方负责。
2. `SubmitDocument(ctx, DocumentRequest)` 提交 JSON 单据。必需字段包括 `documentId`、`documentSN`、Unix 秒 `startTime`、`fields`；结构化发票放入 `invoices`，使用上传返回的 `evidenceAttachmentId`。金额用 `json.Number` 表示。`DocumentField` 的 `type` 使用 Seal AI 协议值，如 `TEXT`、`NUMBER`、`DATE`、`AMOUNT`；若要提交附件字段，其 `value` 应为 `[]AttachmentInfo`。
3. 返回的 `SubmitResponse.AcceptedInvoices` 可标明 `accepted` 或 `reused`。客户端不会自动重试提交；调用方应以稳定 `documentId` 和 `sourceInvoiceId` 控制重试及去重。

HTTP 失败只返回状态码，不在错误中包含响应正文、密钥或票据内容。成功响应限制为 1 MiB。测试租户已验证上传原件及提交普通文档；上传响应可能在顶层或 `attachments[]` 返回附件元数据，客户端兼容这两种形状。上游映射器要求完整元数据并发送 `ATTACHMENT` 字段；只有附件 ID 而无元数据时停止提交。

`MockCallback` 只接受本机请求，验证 `documentId`、`approvalRecordId` 和三态 `decision` 后确认收到；它不会回写飞书，也不能作为 SealAI 服务器可达的公网回调。真正回调的鉴权、幂等和回写须在部署公网入口后单独实现。`bridge.go` 的 `CallbackProcessor` 保留此边界。
