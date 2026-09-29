### 基本信息

- **请求方法**: POST
- **端点**: `https://mediastorm-test.sealai.cc/api/v1/integrations/webhook/wh_1790692906316_6z2eao6/manual-result`
- **说明**: 外部系统同步人工审批结果到 AI 系统（Seal AI 提供，集成方调用）

### 请求头 (Headers)

```
Authorization: Bearer <token>
```

其中 `<token>` 为 Webhook 配置中的鉴权密钥（Bearer Token），调用方需在请求头中携带。

### 请求参数

```typescript
{
  documentId: string               // 外部系统单据 ID
  decision: "approve" | "reject"   // 人工决策
  approver: Approver               // 审批人信息
  rejectComment?: string           // 驳回原因（可选；仅 decision="reject" 且内容非空时保存）
  timestamp: number                // 审批时间（Unix 时间戳，秒）
}
```

> **`rejectComment` 行为说明**: 服务端会先 trim 首尾空白；`decision="approve"` 时忽略该字段；缺失或内容为空白时不覆盖已有驳回原因。

**示例（驳回并附原因）**:
```json
{
  "documentId": "DOC-10001",
  "decision": "reject",
  "approver": {
    "id": "user-001",
    "name": "张三",
    "email": "zhangsan@example.com"
  },
  "rejectComment": "发票金额与费用明细不一致",
  "timestamp": 1787104800
}
```

### Approver 对象结构

```typescript
{
  id: string      // 审批人 ID
  name: string    // 审批人姓名
  email: string   // 审批人邮箱
}
```

### 响应格式

```json
{
  "data": {
    "success": true
  }
}
```

---

### 安全验证

| 项目 | 说明 |
|------|------|
| **鉴权方式** | Bearer Token |
| **说明** | 在 Authorization 头中携带 `Bearer <token>`，token 为 Webhook 配置中的鉴权密钥 |
