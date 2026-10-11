# 阶段记录：飞书通知通道建设与死信自动告警卡片实测验收

- **日期**：2026-10-11
- **责任模块**：`internal/feishu/notify`, `internal/app/recognition`, `cmd/server`, `internal/config`
- **关联版本**：`v0.3.2+` (`[Unreleased]`)

---

## 一、 背景与业务目标

在上一阶段（v0.3.2）中，系统实现了**死信保护机制**（连续失败 3 次自动挂起并停止重试，防止产生无效第三方 OCR 扣费）。然而，死信产生后仅能通过终端命令（`list-failed-receipts`）被动巡检，缺乏主动感知能力。

本阶段目标：
1. 构建通用的**飞书机器人通知通道**（`internal/feishu/notify`），作为系统的标准消息/告警基础设施；
2. 接驳海外小票识别死信流程，在单据连续失败 3 次时，由后台自动向管理员/群聊推送品红色交互式告警卡片；
3. 支持单据一键跳转飞书多维表格对应记录，并具备 1 小时内相同文件的防轰炸去重机制；
4. 提供开箱即用的连通性诊断命令（`test-notify`），便于现场即时验证通道畅通性。

---

## 二、 架构与设计决策

### 1. 接收目标设计：群聊为主，个人为备
- **飞书群聊 (`chat_ids`)**：调用 `im/v1/messages?receive_id_type=chat_id`，将 ebot 机器人拉入运维或财务群即可，群内多人协同，避免单人离岗漏看；
- **个人管理员 (`open_ids`)**：调用 `im/v1/messages?receive_id_type=open_id`，直达个人私信；
- **不采用用户组 (User Group)**：飞书发消息 API 原生不支持向用户组单发，需递归拉取通讯录，受限且沉重。

### 2. 身份与凭证复用
- 统一使用 `feishu.app_id` 与 `feishu.app_secret` 获取 Tenant Access Token，复用 `internal/feishu/token` 的统一并发安全内存缓存，不依赖额外的自定义 Webhook 机器人密钥。

### 3. 配置收敛于 `configs/config.toml`
```toml
[notification]
enabled = true              # 开启通道
chat_ids = []               # 群聊列表
open_ids = [
    "ou_d9906271c1cc05281ad48757fb2bb71a" # 谢子豪
]
notify_on_dead_letter = true # 触发死信时告警
```

---

## 三、 实测验收与飞书渲染留痕

### 1. 命令行诊断触发
执行命令：
```bash
./bin/twc-approval-go-bridge test-notify
```

### 2. 接口返回结果
```text
正在向飞书机器人发送连通性测试卡片...
目标用户 (1): ou_d9906271c1cc05281ad48757fb2bb71a

2026/10/11 22:11:24 INFO dispatched notification card to user event=test_connectivity record_id="" open_id=ou_d9906271c1cc05281ad48757fb2bb71a message_id=om_x100b638dde07bca0c39c642a7fb9e0d
  ✅ [open_id] ou_d9906271c1cc05281ad48757fb2bb71a -> 发送成功 (Message ID: om_x100b638dde07bca0c39c642a7fb9e0d)

全部测试卡片投递成功！
```

### 3. 客户端呈现确认
在持卡人/管理员（谢子豪）的飞书桌面端中，自建应用机器人（“影视飓风”）会话窗口成功接收并实时渲染蓝色诊断卡片：
- **卡片标题**：`【系统通知】飞书告警通道连通性测试`
- **卡片要素**：包含测试触发时间（`2026-10-11 22:11:24`）、触发主体（`CLI 运维诊断 (test-notify)`）与规范署名。
- **渲染状态**：UI 排版、卡片圆角、高亮样式完全符合飞书 Interactive Card 规范，状态良好。

---

## 四、 后续运作与生产部署建议

1. **常驻服务（`server`）自动守护**：
   - 识别流程与通知通道已完成装配，当后台检测到任何小票连续失败 3 次触发死信时，系统会自动发出品红色警告卡片（附带直达单据多维表格的跳转按钮与最后错误原因）；
2. **群聊扩展**：
   - 生产环境建议建一个群（如“海外小票与机审运维群”），将机器人拉进群后，在 `configs/config.toml` 的 `chat_ids` 加上对应 `oc_xxx` 即可，群聊与个人私信互不干扰。
