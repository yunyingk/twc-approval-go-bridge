# 飞书审批定义快照

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
