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

5. **原生 CLI 命令 `notify-transaction`**：
   - 彻底废除临时外部脚本，在 `cmd/server/notify_transaction.go` 内置一等公民命令：
     `./bin/twc-approval-go-bridge notify-transaction <transaction-record-id>`
   - 自动读取 `config.toml` 与业务表配置，定位流水记录，解析持卡人与交易事实，拼接带预填的表单 URL 并直接调用卡片客户端下发。

## 联调与实测投递

1. **机器人能力与版本发布**：
   - 飞书开放平台开通 `im:message:send_as_bot` 权限，并添加「机器人」应用能力发布版本；
   - 查询飞书机器人状态返回 `activate_status: 2`，应用名称为「影视飓风」。
2. **向持卡人谢子豪成功投递**：
   - 真实流水记录：`reczz28LDCeVjW2H`（商户：`OPENAI *CHATGPT SUBSCR SAN FRANCISCO USA`，金额：`CNY 304.30`，持卡人：谢子豪，OpenID：`ou_d9906271c1cc05281ad48757fb2bb71a`）；
   - 执行原生命令：`./bin/twc-approval-go-bridge notify-transaction reczz28LDCeVjW2H`；
   - 飞书接口成功返回 `message_id: "om_x100b63be16490ca0c45507b5d450530"`，卡片成功送达谢子豪飞书会话，按钮链接完美附带 `?prefill_关联交易流水号=reczz28LDCeVjW2H`。

## 验收

- 原生命令 `./bin/twc-approval-go-bridge notify-transaction <record-id>` 联调实测 100% 成功；
- `internal/feishu/card` 单元测试通过；
- `check-business-config` 线上只读核验 100% 成功；
- `go test ./...` 与 `go test -tags no_anthropic ./...` 全量 100% 通过；
- `go vet ./...` 检查通过。
