# 飞书消息卡片模块 (internal/feishu/card) 实现与易商卡补票通知复刻

2026-10-09，Asia/Shanghai。针对多维表格中原由公共“多维表格助手”推送的易商卡消费小票补充提醒，在 `internal/feishu/card` 正式实现由自建应用身份承载的飞书交互式消息卡片构造与派发能力。

## 背景与目标

原多维表格中使用原生自动化流程通过官方“多维表格助手”发送提醒，存在以下痛点：
1. 发件人非企业自建应用，品牌与通知归属割裂；
2. 卡片中的表单链接为通用死链接，无法预填流水号；
3. 无法与后续发票识别、台账建立及核销闭环形成联动。

目标：收敛至本服务 `internal/feishu/card`，以当前自建应用凭证发送 1:1 精确复刻样式的交互卡片。

## 模块设计

新建包 `internal/feishu/card`：
1. **`TransactionNotice` 契约**：承载交易流水号、交易时间、商户名称、结算金额、原币金额、表单链接与署名文本。
2. **`BuildTransactionCardMap` / `BuildTransactionCardJSON`**：构造符合飞书交互卡片规范的载荷：
   - 标题栏：`turquoise` 青色模版，标题为“请及时补充海外易商卡消费发票/账单！”；
   - 正文：Markdown 详情排版（流水号、日期、商户、结算金额、原币金额）；
   - 按钮：`[补充发票/账单]` 主色按钮，配置跳转链接；
   - 底部：分割线及辅助署名。
3. **`BuildFormURL` / `FormPrefillURL` 与全局企业 Host 配置**：
   - 在主配置 `[feishu]` 新增 `host`（企业专属 host 网址，如 `https://zyt-test.feishu.cn`），支持去除斜杠及补全 `https://` 协议；
   - 在表配置 `reimbursement_details` 新增 `form_share_token`（如 `shrcnhMrnWHtHGvgc2G1nHIMDwf`）；
   - 自动生成带预填参数的跳转链接：`https://<host>/share/base/form/<token>?prefill_关联交易流水号=<record_id>`，实现持卡人点击直接关联对应的流水记录。
4. **`Client.SendCardToUser`**：调用飞书 `POST /im/v1/messages?receive_id_type=open_id` 发送 `msg_type: "interactive"` 消息。

## 联调发现与权限开通

后台实测调用飞书消息接口发现：
- 目标持卡人真实 Open ID（`ou_20b69e2f8a70fb4d52598ceb230d9181`，朱益涛）已从多维表格成功读取；
- 飞书开放平台拦截提示：当前应用 `cli_aa4b551c9db85be4` 尚未开通应用发消息权限 `im:message:send_as_bot`（或未开启“机器人”功能）；
- 开通地址：`https://open.feishu.cn/app/cli_aa4b551c9db85be4/auth?q=im:message:send,im:message,im:message:send_as_bot&op_from=openapi&token_type=tenant`。

## 验收

- `internal/feishu/card` 单元测试通过；
- `check-business-config` 线上只读核验 100% 成功（流水表 13 字段、明细表 20 字段、台账表 20 字段）；
- `go test ./...` 与 `go test -tags no_anthropic ./...` 全量 100% 通过；
- `go vet ./...` 检查通过。
