### 基本信息

- **请求方法**: POST
- **端点**: 由集成方提供
- **说明**: AI 审核完成后推送结果到外部系统（集成方提供，Seal AI 调用）

### 请求参数

```typescript
{
  documentId: string              // 外部系统单据 ID
  approvalRecordId: string        // 审批记录 ID
  decision: "approve" | "reject" | "review"  // 决策结果
  comment: string                 // 审批意见，包含 AI 决策标签和摘要
  approvalUrl: string             // 审核结果详情页 URL
  rulesResult?: RuleCheckResult[] // 结构化规则结果（预留字段，当前版本暂不返回）
}
```

> `rulesResult` 为预留字段，当前版本暂不返回。完整审核明细请通过 `approvalUrl` 查看。

### RuleCheckResult 结构

```typescript
{
  id: string                               // 规则 ID
  scope: string                            // 适用场景
  description: string                      // 审核要点描述
  strictness: string                       // 严格程度
  checkResult: "PASS" | "FAIL" | "SKIP"    // 检查结果
  reason: string                           // 检查结果的原因说明
  risks: Risk[]                            // 该规则相关的风险列表
}
```

#### Risk 对象结构

```typescript
{
  details: string            // 风险详情描述
  level: "warning" | "error" // 风险等级
  fieldKey?: string          // 风险关联的字段 key（可选）
}
```

### 响应格式

```json
{
  "success": true
}
```
