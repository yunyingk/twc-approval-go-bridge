# 飞书审批定义快照

## 官方接口原文

以下文件于 2026-09-30 从飞书官方 Markdown 文档库直接下载，原始字节未改写。通过 `llms.txt` → `llms-approval.txt` 定位来源。

| 文件 | 来源 |
| --- | --- |
| [`create-approval-definition.md`](create-approval-definition.md) | [创建审批定义](https://open.feishu.cn/document/server-docs/approval-v4/approval/create.md)，包含完整请求体、响应、权限和使用限制 |
| [`approval-definition-form-controls.md`](approval-definition-form-controls.md) | [审批定义表单控件参数](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/approval-v4/approval/approval-definition-form-control-parameters.md)，包含明细、附件等控件协议及不支持的类型 |

校验值见上一级 [`README.md`](../README.md)。项目编写的模板请求示例放在 `configs/feishu/`，不混入此原文目录。

2026-10-05 另直接下载并保留三份官方 Markdown 原文：[`create-approval-instance.md`](create-approval-instance.md)、[`get-approval-instance.md`](get-approval-instance.md) 和 [`approval-instance-form-controls.md`](approval-instance-form-controls.md)。查询文档明确允许用创建时的 UUID 作为 `instance_id`；创建文档明确 UUID 冲突返回 `60012`，不能将该响应推断成“没有建单”。身份和审批状态也以这些原文为准。

同日从 `llms-approval.txt` 的文件目录取得 [`upload-approval-file.md`](upload-approval-file.md)，并跟随原文链接取得 [`common-errors.md`](common-errors.md)。上传使用 `www.feishu.cn/approval/openapi/v2/file/upload`，不是 v4 实例端点；multipart 的 name/type/content 及目标应用 tenant token 以原文为准。文档中 12 小时期限指下载 URL，没有给出 file code 的 TTL、幂等上传或查询上传结果协议。两份原文按字节保存，保留供应商原始排版。

同日通过官方 llms.txt → llms-approval.txt 直接取得 [`approval-event-overview.md`](approval-event-overview.md)、[`approval-instance-event.md`](approval-instance-event.md) 和 [`subscribe-approval-events.md`](subscribe-approval-events.md)，按字节保存，SHA-256 见上一级表格。原生 approval_instance 事件使用 1.0 envelope，顶层 uuid 是事件 ID，event.uuid 是创建 UUID；官方同时给出 SDK 长连接例子。REVERTED 的 operate_time 与其他状态类型不同，事件数据不能替代实际查询。开发者后台注册/权限以外还需调用模板订阅接口；1390007 同时描述已订阅或已取消，不能推断当前有效。获取实例原文的 reverted 是独立 boolean，而不是上述五个 GET status 的新枚举。

## 当前企业模板

[`approval-definition-9944A2AE-ED45-43F3-9B87-0F3902F09844.json`](approval-definition-9944A2AE-ED45-43F3-9B87-0F3902F09844.json) 是 2026-09-30 使用企业应用 `cli_aaea8d481d381bee` 调用飞书官方接口取得的完整响应，原始字节未改写。这是当前审批模板的资源快照，不是 OpenAPI 规范文件。

官方接口：[查看审批定义](https://open.feishu.cn/document/server-docs/approval-v4/approval/get)。本次请求为：

```text
GET https://open.feishu.cn/open-apis/approval/v4/approvals/9944A2AE-ED45-43F3-9B87-0F3902F09844?locale=zh-CN&user_id_type=open_id&with_option=true
Authorization: Bearer <企业应用的 tenant_access_token>
```

该应用由用户提供为「云间未来」企业租户的应用；企业名称查询因缺少 `tenant:tenant:readonly` 未验证。凭证有效，目标定义名称为「海外易商卡」、状态为 `ACTIVE`，与此前浏览器中读取的控件 ID 和字段名称一致。后台编辑 URL 中的数字 `7664557966711868699` 作为此接口的 `approval_code` 会返回 `1390002`，不能代替上面的 UUID Code。

`data.form` 是 JSON 字符串，包含一个必填的 `fieldList`「明细」（`widget17857436889260001`），其 14 个子控件如下；完整选项、默认值、控件关系和流程节点保留在原始响应中。

| 字段 | 控件 ID | 类型 | 必填 |
| --- | --- | --- | --- |
| 持卡人 | `widget17857435710430001` | `contact` | 是 |
| 卡号后四位 | `widget17857759895070001` | `input` | 是 |
| 交易流水号 | `widget17857760102730001` | `input` | 是 |
| 交易时间 | `widget17857760496230001` | `date` | 是 |
| 商户名称 | `widget17857435583590001` | `textarea` | 是 |
| 交易金额(原币) | `widget17857435953930001` | `amount` | 是 |
| 记账金额CNY | `widget17857760872740001` | `amount` | 是 |
| 交易国家与地区 | `widget17857435576080001` | `textarea` | 是 |
| 交易类型 | `widget17857761303240001` | `input` | 是 |
| 项目 | `widget17857761464580001` | `input` | 是 |
| 费用类目 | `widget17857761467250001` | `input` | 是 |
| 发票账单附件 | `widget17846173054180001` | `attachmentV2` | 否 |
| 供应商是否白名单 | `widget17858345292400001` | `input` | 是 |
| 发票重复判断 | `widget17858352721280001` | `input` | 是 |

此快照确认读取链路和当前结构；审批文件上传、创建实例和审批结果回写尚未通过本项目验证。

## 个人版创建测试

2026-09-30 使用个人版应用 `cli_aa39f558f7799cbb`，通过独立 `cmd/approval-template` 创建「海外易商卡-接口测试」，返回：

```json
{"approval_code":"EA296788-7BFC-47A2-91D7-6B7D8D2D0B11","approval_id":"7691166041770233028"}
```

[`approval-definition-EA296788-7BFC-47A2-91D7-6B7D8D2D0B11.json`](approval-definition-EA296788-7BFC-47A2-91D7-6B7D8D2D0B11.json) 是随后通过正式 `DefinitionClient.GetDefinition` 取得的官方完整响应；在 HTTP 层保存原始字节，没有根据 Go 返回类型重新序列化。请求为：

```text
GET https://open.feishu.cn/open-apis/approval/v4/approvals/EA296788-7BFC-47A2-91D7-6B7D8D2D0B11?locale=zh-CN&user_id_type=open_id&with_option=true
Authorization: Bearer <个人版应用的 tenant_access_token>
```

状态 `ACTIVE`，一个明细、14 个子控件、3 个节点。明细系统 ID 为 `widget17907391970`；子控件系统 ID 为 `widget17907391971` 至 `widget179073919714`，与企业模板无关。各控件的 `custom_id` 对应请求示例中的语义 ID，可用来定位字段；「发票账单附件」的系统 ID 为 `widget179073919712`、`custom_id=invoice_files`、类型为 `attachmentV2`。后台入口：[个人版测试审批模板](https://www.feishu.cn/approval/admin/createApproval?id=7691166041770233028)。
