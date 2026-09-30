# 飞书原生审批

`definitions.go` 实现审批模板（审批定义）的创建和读取，复用飞书官方 Go SDK 的完整请求类型。`approvals.go` 继续定义分组草稿、创建审批实例和读取审批结果的接口；实例业务尚未实现。多维表格的记录和附件操作位于 `../base/`；SealAI 的审核单据位于 `internal/seal/`。

个人版多维表格与「云间未来」企业审批使用不同的应用身份。现有 `FEISHU_APP_ID` / `FEISHU_APP_SECRET` 继续用于个人版 Base；企业审批凭证单独记录为本机 `.env` 的 `FEISHU_APPROVAL_APP_ID` / `FEISHU_APPROVAL_APP_SECRET`，模板 Code 记录为 `FEISHU_APPROVAL_CODE`。模板工具显式选择凭证组；客户端自行获取该应用的 `tenant_access_token`，不切换用户身份。服务配置加载器尚未接入企业审批配置，模板创建也不进入服务启动流程。

2026-09-30 已通过企业应用读取「海外易商卡」的官方定义：`approval_code=9944A2AE-ED45-43F3-9B87-0F3902F09844`，状态 `ACTIVE`，一个 `fieldList` 明细及 14 个子控件。完整原始响应与字段索引见 [`external-api/feishu/`](../../../external-api/feishu/README.md)。旧业务资料中的 5 字段映射不能直接用于当前模板。

## 一次性创建模板

独立入口为 [`cmd/approval-template`](../../../cmd/approval-template/main.go)，不依赖 Anyreceipt、SealAI 或轮询任务。

```bash
# 仅校验请求，不需要凭证，不向飞书发送请求
go run ./cmd/approval-template -app personal -file configs/feishu/approval-template.example.json

# 注入 FEISHU_APP_ID / FEISHU_APP_SECRET 后，在个人版应用下创建
go run ./cmd/approval-template -app personal -file configs/feishu/approval-template.example.json -apply

# 生产时改为 enterprise，使用 FEISHU_APPROVAL_APP_ID / FEISHU_APPROVAL_APP_SECRET
# go run ./cmd/approval-template -app enterprise -file <审核后的模板JSON> -apply
```

程序不自动加载 `.env`。`-app` 必填，选定凭证缺失时直接报错，不回退到另一应用。创建成功输出 `approval_code` 和 `approval_id`，后续保存 Code 来读取模板或创建审批实例。工具没有自动重跑或服务启动调用；请求发送后若响应丢失，应先核查审批后台，避免重复创建。

[`configs/feishu/approval-template.example.json`](../../../configs/feishu/approval-template.example.json) 是本项目按官方协议编写的测试配置，**不是官方原文，也不是当前企业模板的复制件**。它包含一个 `fieldList` 明细和 14 个子控件（含真正的 `attachmentV2` 附件控件），审批人由发起人选择，允许后台修改表单和流程。所有业务名称、字段和流程均在 JSON 内，客户端不硬编码这些内容。金额示例默认 CNY，可按生产要求修改币种范围。

调用权限：应用身份具备 `approval:definition` 或 `approval:approval` 任意一个。2026-09-30 个人版应用 `cli_aa39f558f7799cbb` 初次创建返回 `99991672`，开通权限后完成真实创建。联系人控件首次校验返回 `1390001: widget contact value can not be nil`，因此示例显式配置 `value: {"ignore": false, "multi": false}`，不能依赖文档所述可选默认值。

个人版测试模板「海外易商卡-接口测试」：Code 为 `EA296788-7BFC-47A2-91D7-6B7D8D2D0B11`，ID 为 `7691166041770233028`。同一客户端的 `GetDefinition` 已读取验证：状态 `ACTIVE`，一个 `fieldList` 明细、14 个子控件及 3 个流程节点，字段类型和必填设置与配置一致；「发票账单附件」为 `attachmentV2`。完整官方响应见 [`external-api/feishu/`](../../../external-api/feishu/README.md)。仅创建模板，未发起审批实例。

飞书在创建时分配 `widget...` 系统控件 ID，将请求内的语义 ID 保存为 `custom_id`。填充实例时应重新读取模板并使用返回的系统 `id`，可按 `custom_id` 匹配业务字段，不能直接拿示例中的 `cardholder`、`invoice_files` 等值当系统 ID。

## 官方协议边界

- `POST /open-apis/approval/v4/approvals` 不传 `approval_code` 才是新建；传入 Code 会全量覆盖已有模板。本模块的 `CreateDefinition` 拒绝传入 Code，更新能力以后另设入口。
- `form.form_content` 是包含控件数组的 **JSON 字符串**；明细创建参数里的子控件放在 `value`，不能直接把查询响应里的 `children` 当作创建参数。
- API 创建的模板无法在后台或通过 API 停用、删除；接口不支持条件分支，也不支持 `mutableGroup` 引用多维表格等控件。复杂模板需要审批后台设计。
- 附件控件定义不代表已经上传文件；上传审批附件、填充实例、结果回写仍是独立的后续能力。

完整官方创建文档与控件文档原样归档在 [`external-api/feishu/`](../../../external-api/feishu/README.md)。
