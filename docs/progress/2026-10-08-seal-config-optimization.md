# SealAI 配置拆分优化与 Token 鉴权机制说明

## 实施背景

在以往配置中，`[seal]` 小节直接配置了完整且冗长的文档推送端点：
`document_url = "https://mediastorm-test.sealai.cc/api/v1/integrations/webhook/wh_1790692906316_6z2eao6/document"`。
其中固定包含了 SealAI 系统的固定 Webhook 路由模式 `/api/v1/integrations/webhook/{webhookId}/document`，且与系统内的两个凭证 `bearer_token` 和 `callback_token` 混在一起，配置人员阅读不直观，也不易理解两个 Token 的具体角色和安全边界。

本阶段对 `[seal]` 进行了人机工程学优化与架构梳理：
1. 将晦涩的长 URL 拆解为直观的 `base_url`（服务地址）与 `webhook_id`（通道 ID），由程序在启动加载时自动组装标准 `/document` 与推导 `/attachments` 端点，同时保持对现有 `document_url` 的完全后向兼容；
2. 彻底理清并详细记录 `bearer_token`（送审身份令牌）与 `callback_token`（回调防伪令牌）的单向数据流与安全职责，并在配置模板与生产配置中提供详实中文注释。

---

## 核心机制与 Token 作用说明

### 1. `bearer_token`（送审身份令牌 / 门禁卡）
- **流向**：本桥接服务（Bridge） $\rightarrow$ SealAI 服务端（外部审核引擎）
- **触发场景**：当多维表格产生报销明细（或小票 OCR 识别完成自动送审）时，Bridge 发起 HTTP POST 请求将单据与发票附件提交至 SealAI。
- **协议实现**：在向 SealAI 的 `/document` 及 `/attachments` 接口发送请求时，HTTP Header 中携带：
  ```http
  Authorization: Bearer <bearer_token>
  ```
- **核心职能**：这是 SealAI 分配给企业的客户端接入凭证。Seal 藉此识别请求来源是“影视飓风”，确认调用者具备该 Webhook 通道的送审权限，并扣减对应的企业机审额度。没有它，SealAI 会直接拒绝接入（401 Unauthorized）。

### 2. `callback_token`（回调防伪令牌 / 安全暗号）
- **流向**：SealAI 服务端 $\rightarrow$ 本桥接服务（Bridge）
- **触发场景**：SealAI 在后台运行完规则引擎和大模型深度分析后，需要将机审结果（通过/拒绝/疑点说明等）异步通知给本服务。
- **协议实现**：
  - 本服务在公网暴露了一个专用的接收路由：`POST /seal/callback/{token}`；
  - 在 SealAI 控制台配置 Webhook 回调地址时，填写的也是包含此 token 的专属安全地址：`https://<公网域名>/seal/callback/<callback_token>`；
  - 收到回调请求时，Bridge 内部通过固定时间比对（`subtle.ConstantTimeCompare`）校验请求路径中的 `{token}` 是否与本地配置完全匹配。
- **核心职能**：由于该端点必须暴露在公网供第三方系统调用，如果没有强防伪机制，任何公网恶意扫描器或攻击者都能随意伪造 JSON 请求，谎称“审批通过”从而篡改多维表格中的报销决策。因此系统要求 `callback_token` 必须为至少 32 位的 URL-safe 强随机安全字符串。

---

## 实施改动

1. **配置解析结构升级（`internal/config/config.go`）**：
   - `SealSettings` 结构体扩展：新增 `BaseURL`、`Host`、`WebhookID`，`DocumentURL` 设置为可选；
   - 在 `LoadFile` 中自动合成：
     - 若未配置 `document_url`，但配置了 `webhook_id` 与 `base_url`/`host`，自动规范化并拼接为标准 `${base_url}/api/v1/integrations/webhook/${webhook_id}/document`；
     - 若 `base_url`/`host` 未携带协议头，自动补全 `https://`，并剔除末尾多余斜杠；
     - 严格校验：若只填了 `webhook_id` 未填宿主，或只填了宿主未填 `webhook_id`，立即抛出明确配置错误；
     - 保留 `document_url` 直接配置的兼容支持。
2. **测试用例覆盖（`internal/config/config_test.go`）**：
   - 新增 `TestLoadFile_SealBaseURLAndWebhookID`：覆盖 `base_url` + `webhook_id`、无协议 `host` + `webhook_id`、传统 `document_url` 直传、以及缺少宿主或通道 ID 时的严格报错校验。
3. **配置文件与示例规范化（`configs/config.toml` & `configs/config.example.toml`）**：
   - 将原单行 `document_url` 改造为直观的 `base_url` 与 `webhook_id`；
   - 为 `bearer_token` 与 `callback_token` 补充详尽行级中文注释。

---

## 验证结论

- **单元测试**：
  - `go test ./...` 全量通过；
  - `go test -tags no_anthropic ./...` 独立构建模式通过。
- **代码规范**：
  - `go vet ./...` 零告警。
- **线上联调检查**：
  - `./bin/twc-approval-go-bridge check-business-config` 真实多维表格核验通过，流水表、明细表、台账表共 52 个字段与 Seal 配置全部校验无误。
