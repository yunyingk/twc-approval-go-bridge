### 基本信息

- **请求方法**: POST
- **端点**: `https://mediastorm-test.sealai.cc/api/v1/integrations/webhook/wh_1790692906316_6z2eao6/document`
- **说明**: 外部系统提交单据触发 AI 审核（Seal AI 提供，集成方调用）

### 请求头 (Headers)

```
Authorization: Bearer <token>
```

其中 `<token>` 为 Webhook 配置中的鉴权密钥（Bearer Token），调用方需在请求头中携带。

### 请求参数

```typescript
{
  documentId: string              // 外部系统单据 ID
  documentSN: string              // 单据编号
  documentURL?: string            // 单据链接（可选）
  startTime: number               // Unix 时间戳（秒）
  fields: DocumentField[]         // 单据字段列表
  invoices?: ExternalInvoice[]    // 结构化发票列表（可选，见下方「结构化发票」章节）
  disableAttachmentCache?: boolean // 不使用附件缓存，默认 false；仅深度思考模式生效
  thinkingDepthOverride?: "default" | "medium" | "high" // 强制本次思考深度
}
```

> **审核任务控制参数**:
> - `disableAttachmentCache` 未传或传 `null` 时默认为 `false`。设为 `true` 后，本次任务会重新解析所有附件，不复用历史附件解析结果；该参数仅对深度思考模式生效。
> - `thinkingDepthOverride` 可强制本次任务使用指定思考深度：`medium` 为标准模式，`high` 为深度思考模式；`default` 或不传时沿用审核偏好设置。

### 结构化发票（invoices）

当外部系统已经确认并结构化发票信息时，可通过顶层 `invoices` 字段提交，SealAI 会将其作为一等资源接入发票验真、去重与审核链路，不再依赖普通附件猜测识别。每张发票需引用一个由附件上传接口返回的 `attachmentId`（证据原件）。

```typescript
{
  kind: "invoice"                         // 固定判别值
  sourceInvoiceId: string                 // 来源系统内稳定的发票标识（同一 ID 重试幂等；更换发票号码或核心字段会返回 conflict）
  invoiceType: "domestic" | "overseas" | "other"  // 发票类型：国内 / 境外 / 其他
  invoiceCode?: string                    // 发票代码（可选）
  invoiceNumber: string                   // 发票号码
  invoiceDate?: string                    // 开票日期（YYYY-MM-DD，可选）
  currencyCode: string                    // 币种（ISO 4217，如 CNY、EUR）
  totalAmount: number                     // 票面含税金额（原币，必须是 JSON number）
  taxAmount?: number                      // 税额（原币，可选）
  amountWithoutTax?: number               // 不含税金额（原币，可选）
  seller: {
    name: string                          // 销售方名称
    taxNumber?: string                    // 销售方税号（可选）
    countryCode?: string                  // 销售方国家代码（可选）
  }
  buyer: {
    name: string                          // 购买方名称
    taxNumber?: string                    // 购买方税号（可选）
    countryCode?: string                  // 购买方国家代码（可选）
  }
  claimedAmount?: number                  // 本次单据使用金额（可选，大于 0 且不超过 totalAmount）
  expenseRefs?: Array<{                   // 费用分摊引用（可选；单据存在费用明细时必须提供且完整关联）
    sourceExpenseId: string               // 费用明细行的稳定标识（须与 fields 中费用明细行的 detailId 对应）
    allocatedAmount: number               // 该明细分摊的发票金额
    currencyCode: string                  // 分摊币种（须与发票币种一致）
  }>
  evidenceAttachmentId: string            // 证据原件 attachmentId（由附件上传接口返回，wat1.xxx 形式）
}
```

> **无条件必填字段**: 除固定判别值 `kind` 外，`sourceInvoiceId`、`invoiceType`、`invoiceNumber`、`currencyCode`、`totalAmount`、`seller.name`、`buyer.name`、`evidenceAttachmentId` 这 8 个业务字段对三种 `invoiceType` 均为必填。`invoiceDate`、`seller.countryCode`、`claimedAmount`、双方税号、`invoiceCode`、`taxAmount`、`amountWithoutTax` 对其余字段均为可选；没有数据的可选文本字段请省略或传 `null`，不要使用无业务含义的占位值。
>
> **可选字段缺省行为**: 缺失开票日期在系统内保持缺省语义，数据库中存储为 `NULL`；缺失 `claimedAmount` 且没有 `expenseRefs` 时不登记单据级占用金额，只完成发票入库与单据审核关联。
>
> **金额字段类型**: `totalAmount`、`taxAmount`、`amountWithoutTax`、`claimedAmount`、`expenseRefs[].allocatedAmount` 必须是 JSON `number`；如原始数据为 `"100.50"`、`"￥100.50"`、`"1,000.00"` 等字符串，请由集成方中间层先清洗转换，SealAI 不接受数字字符串。

**示例（境外发票）**:
```json
{
  "kind": "invoice",
  "sourceInvoiceId": "customer-invoice-16153",
  "invoiceType": "overseas",
  "invoiceNumber": "INV-16153",
  "invoiceDate": "2026-05-26",
  "currencyCode": "AUD",
  "totalAmount": 5115,
  "taxAmount": 465,
  "amountWithoutTax": 4650,
  "seller": {
    "name": "My Accounting Services",
    "taxNumber": "73 050 035 311",
    "countryCode": "AU"
  },
  "buyer": {
    "name": "Growatt New Energy Australia Pty Ltd",
    "countryCode": "AU"
  },
  "claimedAmount": 5115,
  "expenseRefs": [
    {
      "sourceExpenseId": "expense-line-6",
      "allocatedAmount": 5115,
      "currencyCode": "AUD"
    }
  ],
  "evidenceAttachmentId": "wat1.xxx"
}
```

**示例**:
```json
{
  "kind": "invoice",
  "sourceInvoiceId": "customer-invoice-10002",
  "invoiceType": "domestic",
  "invoiceNumber": "INV-10002",
  "currencyCode": "CNY",
  "totalAmount": 200,
  "seller": {
    "name": "示例销售方"
  },
  "buyer": {
    "name": "示例购买方"
  },
  "claimedAmount": 150,
  "expenseRefs": [
    {
      "sourceExpenseId": "expense-line-001",
      "allocatedAmount": 150,
      "currencyCode": "CNY"
    }
  ],
  "evidenceAttachmentId": "wat1.xxx"
}
```

> 带费用分摊时，单据 `fields` 中必须存在来源标识为 `expense-line-001` 的费用明细行。

> **⚠️ 发票使用约定**:
> - `invoices` 与 `fields` 中的费用明细通过 `expenseRefs.sourceExpenseId` ↔ 费用明细行的 `detailId` 关联；若单据存在费用明细，必须提供 `expenseRefs`。
> - 同一 `sourceInvoiceId` 重试会复用已有发票，但更换发票号码或核心字段（发票类型、币种、票面金额、销方/购方名称等）会返回 conflict。
> - 同一发票用于不同单据时，按新单据补充认领/分摊；同一单据重试时，`claimedAmount` 与 `expenseRefs` 必须和首次提交一致，否则返回 conflict。
> - 不同来源 ID 提交相同 `invoiceNumber` 直接返回 conflict（不复用、不覆盖来源关系），无论发票代码或其他发票事实是否一致。
> - 对未绑定来源的历史发票，服务端会先按租户和票号读取候选，再校验发票代码及核心事实；历史记录无法确认发票类型时不会自动绑定来源。
> - 相同来源 ID 在不同单据中复用同一发票时，仍由现有发票占用/重复报销链路处理。
> - 发票无需再次从附件识别；`evidenceAttachmentId` 对应的原件仅用于审核展示与归档。

### DocumentField 数据结构

#### 基本结构

```typescript
{
  key: string              // 字段唯一标识
  label: string            // 字段展示名称
  type: DocumentFieldType  // 字段类型（见下方类型列表）
  value: any               // 字段值（根据 type 不同，格式不同）
  comments?: string        // 字段备注说明（可选，如汇率、时区等额外信息）
  semanticType?: "RELATED_DOCUMENT_LIST" | "EXPENSE_DETAIL_LIST" | "GENERIC_LIST"
                           // LIST 字段的业务语义；提交结构化发票时，非空二维 LIST 必填
}
```

#### 支持的字段类型

##### TEXT (文本字段)

**值类型**: `string`

**示例**:
```json
{
  "key": "description",
  "label": "报销说明",
  "type": "TEXT",
  "value": "客户商务午餐"
}
```

---

##### NUMBER (数字字段)

**值类型**: `number`

**示例**:
```json
{
  "key": "days",
  "label": "出差天数",
  "type": "NUMBER",
  "value": 3
}
```

---

##### DATE (日期字段)

**值类型**: `string` (RFC3339格式)

**示例**:
```json
{
  "key": "expense_date",
  "label": "报销日期",
  "type": "DATE",
  "value": "2024-01-15"
}
```

---

##### AMOUNT (金额字段)

**值类型**: `AmountValue`

**结构**:
```typescript
{
  amount: number     // 金额数值
  currency: string   // 货币代码（如 CNY、USD）
}
```

**示例**:
```json
{
  "key": "total_amount",
  "label": "报销金额",
  "type": "AMOUNT",
  "value": {
    "amount": 1000.5,
    "currency": "CNY"
  }
}
```

---

##### PERSON (人员字段)

**值类型**: `PersonValue | PersonValue[]` （支持单个对象或数组）

**结构**:
```typescript
{
  sourceId: string         // 来源系统的人员 ID
  name: string             // 人员姓名
  department?: string      // 部门路径（可选，如 'AAA/BBB/CCC'）
  sourceSystem: string     // 来源系统
  userId?: string          // 关联到系统中的 User ID（可选）
  mobile?: string          // 手机号（可选，建议提供）
  email?: string           // 邮箱（可选，建议提供）
}
```

> **⚠️ 注意**: 至少提供 `mobile` 或 `email` 之一，用于更好的用户识别和匹配

**示例（单个人员）**:
```json
{
  "key": "applicant",
  "label": "申请人",
  "type": "PERSON",
  "value": {
    "sourceId": "user_001",
    "name": "张三",
    "department": "公司/财务部/会计组",
    "sourceSystem": "webhook",
    "mobile": "+8613800138000",
    "email": "zhangsan@example.com"
  }
}
```

**示例（多个人员）**:
```json
{
  "key": "cc_list",
  "label": "抄送人",
  "type": "PERSON",
  "value": [
    {
      "sourceId": "user_002",
      "name": "李四",
      "mobile": "+8613800138001"
    },
    {
      "sourceId": "user_003",
      "name": "王五",
      "email": "wangwu@example.com"
    }
  ]
}
```

---

##### ATTACHMENT (附件字段)

**值类型**: `AttachmentValue[]` （数组）

**结构**:
```typescript
{
  name: string           // 文件名
  mimeType: string       // 文件类型（MIME type）
  url: string            // 临时访问链接（由上传接口返回，24小时有效）
  ossPath: string        // OSS 存储路径（用于重新生成临时链接）
  ossSignedUrl: string   // 存储服务临时访问链接（与 url 一致）
  ossFileSize: number    // 文件大小（字节）
}
```

> **⚠️ 注意**: 附件需先调用附件上传接口获取 `AttachmentValue` 对象

**示例**:
```json
{
  "key": "receipts",
  "label": "报销凭证",
  "type": "ATTACHMENT",
  "value": [
    {
      "name": "发票.pdf",
      "mimeType": "application/pdf",
      "url": "https://oss.example.com/temp/xxx?signature=...",
      "ossPath": "tenants/tenant_001/attachments/webhook_wh_xxx_550e8400-e29b-41d4-a716-446655440000/发票.pdf",
      "ossSignedUrl": "https://oss.example.com/temp/xxx?signature=...",
      "ossFileSize": 1048576
    }
  ]
}
```

**附件上传流程**:
1. 调用附件上传接口，上传文件到系统
2. 接口返回 `AttachmentInfo` 对象（包含 `url`, `ossPath` 等字段）
3. `ATTACHMENT` 类型字段的值为 `AttachmentValue[]`，你可以将接口返回的 `AttachmentInfo` 对象作为 `AttachmentValue` 使用

> 注意：`ossPath` 由服务端生成且每次上传唯一（含内部对象 ID），请将其视为**不透明值**原样保存和回传；不要从中解析文件名，也不要依赖其目录结构。同一文件名多次上传会得到不同的 `ossPath`，互不覆盖。

---

##### DATE_RANGE (日期范围字段)

**值类型**: `DateRangeValue`

**结构**:
```typescript
{
  start: string       // 开始日期（RFC3339格式）
  end: string         // 结束日期（RFC3339格式）
  interval?: number   // 时长（天数，可选）
}
```

**示例**:
```json
{
  "key": "travel_dates",
  "label": "出差日期",
  "type": "DATE_RANGE",
  "value": {
    "start": "2024-01-15",
    "end": "2024-01-20",
    "interval": 5
  }
}
```

---

##### DEPARTMENT (部门字段)

**值类型**: `DepartmentValue | DepartmentValue[]` （支持单个对象或数组）

**结构**:
```typescript
{
  id: string              // 部门在来源系统中的 ID
  name: string            // 部门名称
  path?: string           // 部门路径（可选，如 'AAA/BBB/CCC'）
  sourceSystem: string    // 来源系统
}
```

**示例（单个部门）**:
```json
{
  "key": "department",
  "label": "部门",
  "type": "DEPARTMENT",
  "value": {
    "id": "dept_001",
    "name": "财务部",
    "path": "公司/财务部",
    "sourceSystem": "webhook"
  }
}
```

**示例（多个部门）**:
```json
{
  "key": "cost_centers",
  "label": "成本中心",
  "type": "DEPARTMENT",
  "value": [
    {
      "id": "dept_001",
      "name": "产品部",
      "path": "公司/产品部"
    },
    {
      "id": "dept_002",
      "name": "技术部",
      "path": "公司/技术部"
    }
  ]
}
```

---

##### LIST (列表字段)

**值类型**: `DocumentField[][]` （二维数组）

`semanticType` 用于明确列表用途，避免服务端根据 `key` 或 `label` 猜测业务语义：

- `EXPENSE_DETAIL_LIST`：费用明细列表。每行必须包含 `key="detailId"` 的非空文本字段，作为该费用行在来源系统中的稳定标识。
- `GENERIC_LIST`：其他普通列表。
- `RELATED_DOCUMENT_LIST`：关联单据列表。

当请求包含顶层 `invoices` 时，所有非空的二维 LIST（包括嵌套 LIST）都必须声明 `semanticType`。发票的 `expenseRefs[].sourceExpenseId` 必须与对应费用行的 `detailId` 完全一致。

**费用明细列表示例**:
```json
{
  "key": "expense_details",
  "label": "费用明细",
  "type": "LIST",
  "semanticType": "EXPENSE_DETAIL_LIST",
  "value": [
    [
      {
        "key": "detailId",
        "label": "费用明细标识",
        "type": "TEXT",
        "value": "expense-line-001"
      },
      {
        "key": "item_name",
        "label": "项目",
        "type": "TEXT",
        "value": "办公用品"
      },
      {
        "key": "item_amount",
        "label": "金额",
        "type": "AMOUNT",
        "value": {
          "amount": 500,
          "currency": "CNY"
        }
      }
    ]
  ]
}
```

对应发票分摊应使用相同标识：

```json
{
  "expenseRefs": [
    {
      "sourceExpenseId": "expense-line-001",
      "allocatedAmount": 500,
      "currencyCode": "CNY"
    }
  ]
}
```

**普通列表示例**:

```json
{
  "key": "travel_companions",
  "label": "同行人员",
  "type": "LIST",
  "semanticType": "GENERIC_LIST",
  "value": [
    [
      {
        "key": "name",
        "label": "姓名",
        "type": "TEXT",
        "value": "李四"
      }
    ]
  ]
}
```

> **常见校验错误**:
> - `结构化发票请求中的 table-list 必须声明 EXPENSE_DETAIL_LIST 或 GENERIC_LIST`：为请求内每个非空二维 LIST 补充正确的 `semanticType`。
> - `EXPENSE_DETAIL_LIST 的每行必须提供稳定 detailId`：在每行加入非空文本字段 `{ "key": "detailId", ... }`，并在重试时保持值不变。

---

##### CITY (城市字段)

**值类型**: `CityValue[]` （数组）

**结构**:
```typescript
{
  key: string     // 城市代码/ID
  label: string   // 城市名称（如 "浙江省/杭州市/西湖区"）
}
```

**示例**:
```json
{
  "key": "city",
  "label": "所在城市",
  "type": "CITY",
  "value": [
    {
      "key": "330106",
      "label": "浙江省/杭州市/西湖区"
    }
  ]
}
```

---

##### OBJECT (自定义对象字段)

**值类型**: `Record<string, string> | Array<Record<string, string>>`

**支持格式**:
1. **键值对象**: `Record<string, string>` - 用于简单的键值对数据（值必须为字符串）
2. **对象数组**: `Array<Record<string, string>>` - 用于结构化列表数据（对象内的值必须为字符串）

**示例（键值对象）**:
```json
{
  "key": "metadata",
  "label": "元数据",
  "type": "OBJECT",
  "value": {
    "project": "AI审核系统",
    "version": "1.0.0",
    "region": "华东"
  }
}
```

**示例（对象数组）**:
```json
{
  "key": "custom_items",
  "label": "自定义项目列表",
  "type": "OBJECT",
  "value": [
    {
      "itemId": "001",
      "itemName": "项目A",
      "quantity": "10",
      "status": "active"
    },
    {
      "itemId": "002",
      "itemName": "项目B",
      "quantity": "5",
      "status": "pending"
    }
  ]
}
```

> **⚠️ 注意**: OBJECT 类型的值必须是字符串。如果原始数据是数字、布尔值等其他类型，请先转换为字符串

---

##### BOOL (布尔字段)

**值类型**: `boolean`

**说明**: 用于表示是/否、真/假等二元状态。

**示例**:
```json
{
  "key": "is_urgent",
  "label": "是否加急",
  "type": "BOOL",
  "value": true
}
```

---

##### OTHER (已废弃)

> **⚠️ 废弃说明**: `OTHER` 类型已不推荐使用，仅为历史兼容性保留。新集成请使用以下明确类型替代：
> - 简单键值对 → 使用 `OBJECT` 类型（`Record<string, string>`）
> - 结构化列表 → 使用 `OBJECT` 类型（`Array<Record<string, string>>`）
> - 城市数据 → 使用 `CITY` 类型（`CityValue[]`）
> - 布尔值 → 使用 `BOOL` 类型

**兼容策略**: 系统仍然接受 `OTHER` 类型的字段，但建议逐步迁移到明确类型以获得更好的类型安全性和验证支持。

---

### 附件上传接口

在发起 AI 审核前，需要先上传附件文件。

#### 端点

```
POST /v1/integrations/webhook/{webhookId}/attachments
```

#### 请求头 (Headers)

```
Content-Type: multipart/form-data
Authorization: Bearer <token>
```

#### 请求体 (multipart/form-data)

```
files: File | File[]  // 单个文件或文件数组
```

#### 响应格式

**单个文件**:
```json
{
  "data": {
    "attachmentId": "wat1.xxx",
    "name": "发票.pdf",
    "mimeType": "application/pdf",
    "url": "https://oss.example.com/temp/xxx",
    "ossPath": "tenants/xxx/attachments/xxx.pdf",
    "ossSignedUrl": "https://oss.example.com/temp/xxx",
    "ossFileSize": 1048576
  }
}
```

**多个文件**:
```json
{
  "data": {
    "attachments": [
      {
        "attachmentId": "wat1.xxx",
        "name": "发票.pdf",
        "mimeType": "application/pdf",
        "url": "https://oss.example.com/temp/xxx",
        "ossPath": "tenants/xxx/attachments/xxx.pdf",
        "ossSignedUrl": "https://oss.example.com/temp/xxx",
        "ossFileSize": 1048576
      }
    ]
  }
}
```

> **💡 attachmentId 用途**：上传附件返回的 `attachmentId`（`wat1.xxx` 形式）用于在提交单据时引用证据原件，即结构化发票中的 `evidenceAttachmentId` 字段。`attachmentId` 为短期不透明引用（默认 24 小时有效），请在有效期内完成单据提交。

---

## 响应格式

```json
{
  "data": {
    "success": true,
    "documentId": "单据在系统内的 ID（可选）",
    "acceptedInvoices": [
      {
        "sourceInvoiceId": "来源系统发票标识",
        "status": "accepted"
      }
    ]
  }
}
```

`acceptedInvoices.status` 取值：
- `accepted`：本次首次受理（含首次绑定到已有同号发票）
- `reused`：该来源发票已受理或已挂接当前单据，本次幂等复用（重试幂等）

> 未提交 `invoices` 时 `acceptedInvoices` 为空数组；`documentId` 可用于后续结果对账。

---

### 安全验证

| 项目 | 说明 |
|------|------|
| **鉴权方式** | Bearer Token |
| **说明** | 在 Authorization 头中携带 `Bearer <token>`，token 为 Webhook 配置中的鉴权密钥 |
