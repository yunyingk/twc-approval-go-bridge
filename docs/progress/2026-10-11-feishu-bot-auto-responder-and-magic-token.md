# 飞书机器人响应式对话交互与 2 小时短授权 Token 实施与验收记录

## 1. 业务背景与用户诉求

在后台监控看板（概览 / 单据审计 / 系统日志）上线后，管理员希望在日常办公场景中能更加自然、便捷地访问系统。具体诉求包含两点：
1. **飞书私信对话交互响应**：管理员在飞书中私信企业机器人（无需@）发送“管理页面”或“看板”等关键词，机器人能够自动识别意图并回复包含直达链接的交互式卡片；同时支持其他辅助指令（如查询系统健康度状态、帮助菜单）。
2. **两小时短授权 Token（Magic Link）**：管理员希望免密直接登录管理后台，但要求安全性高，必须是带有时效性（默认 2 小时）的临时授权凭证，超时后自动失效并引导重新申请。

---

## 2. 关键架构设计与技术选型

### 2.1 飞书长连接多事件复用 (`events.Listener` + `im.message.receive_v1`)
- **长连接单连接多路复用**：利用已有的 WebSocket 持久连接（`larkws`），无需额外开辟网络连接或配置公网 Webhook 回调地址，通过 `events.NewForEvents` 同时注册：
  - `drive.file.bitable_record_changed_v1`（多维表格记录变更事件）；
  - `im.message.receive_v1`（机器人接收消息事件）。
- **意图匹配与文本净化 (`internal/feishu/bot`)**：
  - 自动剥离飞书消息中的 `@mention` 占位符（如 `@_user_1`、`@影视飓风`）；
  - 内置意图分类器：
    - `IntentDashboard`（关键词包含“管理页面”、“看板”、“dashboard”、“后台”、“控制台”）；
    - `IntentStatus`（关键词包含“状态”、“status”、“健康”）；
    - `IntentHelp`（关键词包含“帮助”、“help”、“菜单”、“？”）；
  - 过滤非用户消息（排除 `sender_type: "app"`），杜绝机器人之间自说自话无限死循环；
  - 内存 5 分钟滑动窗口去重，防范网络抖动造成的飞书重复事件投递。

### 2.2 两小时短授权令牌管理 (`TokenManager`)
- **安全临时令牌生成**：每次匹配到“管理页面”请求且通过管理员 OpenID 白名单核验后，通过强伪随机数生成 `twc_m_<hex>` 令牌；
- **生命周期与会话置换机制**：
  1. 用户在飞书收到带参 URL（`https://twc-approval.ying-qing.cn/?token=twc_m_...`）；
  2. 浏览器打开该链接，服务端拦截 `?token=` 参数并由 `TokenManager.Validate()` 核验；
  3. 核验通过后，服务端签发同生命周期（剩余 TTL，最长 2 小时）的 HttpOnly 会话 Cookie（`twc_dash_token`）；
  4. 服务端执行 `303 See Other` 重定向回干净根路径 `/`，将敏感 Token 从浏览器地址栏和 HTTP Referer 中彻底抹除；
  5. 后续会话均受该 Cookie 保护，若 2 小时到期，服务端即刻判定无效并跳出友好提示页面。
- **群聊防剧透安全策略**：若用户在公共群聊中发送 `@影视飓风 管理页面`，系统会自动将包含令牌的直达卡片**单聊私信**发给该用户，并在群内回复安全提醒，避免授权链接暴露给群内其他非授权人员。

---

## 3. 验收与实测验证

### 3.1 单元测试与双构建守卫
- 执行 `go test ./...` 与 `go test -tags no_anthropic ./...` 以及 `go vet ./...` 全量通过；
- 覆盖率涵盖：文本解析、意图匹配、令牌全生命周期（签发、核验、过期、撤销）、HTTP 登录拦截与 Cookie 会话置换、群聊隐私提醒等。

### 3.2 真实飞书消息交互冒烟验证
运行 `./bin/twc-approval-go-bridge test-bot-reply -text "管理页面"`，系统输出：
```text
正在模拟飞书私信交互...
发送者 OpenID: ou_d9906271c1cc05281ad48757fb2bb71a
消息内容: "管理页面"
识别意图: dashboard
✅ 飞书机器人交互响应成功投递！
已生成临时授权凭证:
  • Token: twc_m_2c88d710ac763efe81e4862e6f4861e769c39c46
  • 访问链接: https://twc-approval.ying-qing.cn/?token=twc_m_2c88d710ac763efe81e4862e6f4861e769c39c46
  • 失效时间: 2026-10-12 01:16:55 (有效 2 小时)
```
飞书客户端（谢子豪）成功接收到蓝色授权卡片，点击按钮即可一键免密进入看板。

### 3.3 常驻服务热重启与长连接验证
重启后台 launchd 守护进程后，系统日志显示：
```json
{
  "level": "INFO",
  "message": "Feishu bot interactive responder enabled",
  "attrs": {
    "admin_count": 1,
    "public_url": "https://twc-approval.ying-qing.cn",
    "token_ttl": "2h0m0s"
  }
}
{
  "level": "INFO",
  "message": "Feishu long connection ready",
  "attrs": {
    "event_type": "drive.file.bitable_record_changed_v1,im.message.receive_v1"
  }
}
```
单一长连接同时稳固监听两个核心事件。
