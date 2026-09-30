# 外部接口原始资料

此目录保存供应商发布的接口原文，供 Git 追踪协议变化。原文文件不根据本项目的 Go 结构体补写或改写；项目适配与业务说明仍放在 `internal/` 各模块的 README 中。采集日期：2026-09-30。

| 原文文件 | 官方来源 | SHA-256 |
| --- | --- | --- |
| [`anyreceipt/anyreceipt-openapi-v1.0.7.yaml`](anyreceipt/anyreceipt-openapi-v1.0.7.yaml) | [Anyreceipt 官网提供的 OpenAPI YAML](https://anyreceipt.cn/openapi/anyreceipt-openapi.yaml)，文件内版本 `v1.0.7` | `3db6992b75bbe9859bc4bf6180656cb6df9e3231a761c2e1e7c05bdaa987d1f9` |
| [`sealai/webhook-document.md`](sealai/webhook-document.md) | [SealAI 测试租户 Webhook 通道](https://mediastorm-test.sealai.cc/audit/deploy/webhook)「发起 AI 审核 → 查看参数 → 复制 Markdown」；包含附件上传接口 | `4952cb6d6bd69c96d4430318b62ccae71bc842f0548548c49748cdc658c30a67` |
| [`sealai/webhook-callback.md`](sealai/webhook-callback.md) | 同一通道「推送 AI 审批结果 → 查看参数 → 复制 Markdown」 | `5d98636964f6414b9c475d50c5a90b13eaaf8ec5320d9af1bfe2690bfe276278` |
| [`sealai/webhook-manual-result.md`](sealai/webhook-manual-result.md) | 同一通道「同步人工审核结果 → 查看参数 → 复制 Markdown」 | `a0750d2ea98a1c1c8ae02da2a13620976e8fc31520a7465dae2a3b4cd4f12561` |
| [`feishu/approval-definition-9944A2AE-ED45-43F3-9B87-0F3902F09844.json`](feishu/approval-definition-9944A2AE-ED45-43F3-9B87-0F3902F09844.json) | 飞书官方「查看审批定义」接口的完整响应；企业应用身份，采集详情与字段索引见 [`feishu/README.md`](feishu/README.md) | `0a850b8425cd094a9caf766e5f79e107323bbf7923f251092452379f764bfe04` |
| [`feishu/create-approval-definition.md`](feishu/create-approval-definition.md) | [飞书官方创建审批定义文档的 Markdown 原文](https://open.feishu.cn/document/server-docs/approval-v4/approval/create.md) | `f02362953035fb7fdd80b0cd6c0a7a0e6b9578557579fedc75dea798a5ca3d25` |
| [`feishu/approval-definition-form-controls.md`](feishu/approval-definition-form-controls.md) | [飞书官方审批定义表单控件文档的 Markdown 原文](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/approval-v4/approval/approval-definition-form-control-parameters.md) | `5abb56725aeca5c8a6d100e5aecc59b6dff3a402009d22e5197382fe9d77d29a` |

SealAI 这三份是租户官方页面直接生成的 Markdown，并非 OpenAPI 导出文件；目前未发现该租户提供可下载的完整 OpenAPI 规范。原文中的 Webhook 地址属于当前测试通道，不能当成其他租户的固定地址。原文只含 `Bearer <token>` 占位符，没有提交鉴权密钥。更新时应重新从供应商获取原文，并记录新版本或新的采集日期与校验值。
