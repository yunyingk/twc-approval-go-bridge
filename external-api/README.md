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
| [`feishu/approval-definition-EA296788-7BFC-47A2-91D7-6B7D8D2D0B11.json`](feishu/approval-definition-EA296788-7BFC-47A2-91D7-6B7D8D2D0B11.json) | 飞书官方「查看审批定义」接口的完整响应；个人版应用创建后读取，采集详情见 [`feishu/README.md`](feishu/README.md) | `cb84dbb61fd7b66aaa7cafa19018ee0d31efcc754adbc430aadbae386a559311` |
| [`feishu/create-approval-instance.md`](feishu/create-approval-instance.md) | [飞书官方创建审批实例 Markdown 原文](https://open.feishu.cn/document/server-docs/approval-v4/instance/create.md)，2026-10-05 采集 | `f32fdf3226bcac78302adb491876b2747a8b9b1c8d528c922a12c8aa882512d6` |
| [`feishu/get-approval-instance.md`](feishu/get-approval-instance.md) | [飞书官方获取审批实例 Markdown 原文](https://open.feishu.cn/document/server-docs/approval-v4/instance/get.md)，2026-10-05 采集 | `34617e24630a4152be6dcfd239ec3409a14d37066dc65fcb232f954bcc4fd1ed` |
| [`feishu/approval-instance-form-controls.md`](feishu/approval-instance-form-controls.md) | [飞书官方审批实例控件 Markdown 原文](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/approval-v4/instance/approval-instance-form-control-parameters.md)，2026-10-05 采集 | `e975dd6a0f0ffa9723d8708ed85a12d18edecdfcb24655785947038ba999e127` |
| [`feishu/upload-approval-file.md`](feishu/upload-approval-file.md) | [飞书官方上传审批文件 Markdown 原文](https://open.feishu.cn/document/server-docs/approval-v4/file/upload-files.md)，2026-10-05 采集 | `d6c87115ebae73bcb909551aba05b5db5f24f2d9e69f06ed6551832c8889a3a7` |
| [`feishu/common-errors.md`](feishu/common-errors.md) | [上传文档引用的飞书通用错误码原文](https://open.feishu.cn/document/ukTMukTMukTM/ugjM14COyUjL4ITN.md)，2026-10-05 采集 | `b69fa2480463dbc267f60ae32fddb5bfc9aceeb2ee6ac1449291b80b437effce` |
| [`feishu/approval-event-overview.md`](feishu/approval-event-overview.md) | [飞书官方审批事件概述 Markdown 原文](https://open.feishu.cn/document/server-docs/approval-v4/event/function-introduction.md)，2026-10-05 采集 | `9d139c394ecb8d06328ddbebf525d261da263ec88dbe1be382f225296da888f7` |
| [`feishu/approval-instance-event.md`](feishu/approval-instance-event.md) | [飞书官方原生审批实例事件 Markdown 原文](https://open.feishu.cn/document/server-docs/approval-v4/event/common-event/approval-instance-event.md)，2026-10-05 采集 | `3dfed9cfc886e2fe9f71f7f1c5041a5ab50d2d094503fd698d345a1005c60ee7` |
| [`feishu/subscribe-approval-events.md`](feishu/subscribe-approval-events.md) | [飞书官方订阅审批事件 Markdown 原文](https://open.feishu.cn/document/server-docs/approval-v4/event/event-interface/subscribe.md)，2026-10-05 采集 | `7a8374652b733b2a02468a9a0eb05c5ea5d32b3018aa572f1a5c41d00b8f6b99` |

SealAI 这三份是租户官方页面直接生成的 Markdown，并非 OpenAPI 导出文件；目前未发现该租户提供可下载的完整 OpenAPI 规范。原文中的 Webhook 地址属于当前测试通道，不能当成其他租户的固定地址。原文只含 `Bearer <token>` 占位符，没有提交鉴权密钥。更新时应重新从供应商获取原文，并记录新版本或新的采集日期与校验值。
