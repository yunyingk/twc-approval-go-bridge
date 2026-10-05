# 飞书原生审批

`definitions.go` 实现审批模板（审批定义）的创建和读取，复用飞书官方 Go SDK 的完整请求类型。`approvals.go` 继续定义分组草稿、创建审批实例和读取审批结果的接口；实例业务尚未实现。多维表格的记录和附件操作位于 `../base/`；SealAI 的审核单据位于 `internal/seal/`。

2026-10-05 已新增实例传输客户端与明细表单映射，详见下方；旧 `approvals.go` 是保留的历史接口，不是当前实例实现。共用分组、持久化来源版本映射、实例业务接入和人工结果回写仍需继续实施。

同日后续已实现 `core/approval`、`app/approval` 共用分组/版本/预约/对账，以及本目录的 `gateway.go` 适配。服务配置与真实来源装配、审批上传和人工结果回写尚未完成；上述“仍需实施”保留接口阶段的历史范围。

个人版多维表格与「云间未来」企业审批使用不同的应用身份。现有 `FEISHU_APP_ID` / `FEISHU_APP_SECRET` 继续用于个人版 Base；企业审批凭证单独记录为本机 `.env` 的 `FEISHU_APPROVAL_APP_ID` / `FEISHU_APPROVAL_APP_SECRET`，模板 Code 记录为 `FEISHU_APPROVAL_CODE`。模板工具显式选择凭证组；客户端自行获取该应用的 `tenant_access_token`，不切换用户身份。服务配置加载器尚未接入企业审批配置，模板创建也不进入服务启动流程。

上段保留初次联调的身份安排。当前 Base 已改用新的企业测试应用；它读取旧企业模板返回 `1390002`，旧审批应用仍能读取同一 Code。两个应用的企业身份查询均缺少权限，不能据此确认同租户，也不能自动把 Base 的 `open_id` 用于旧审批应用。

## 实例客户端与表单映射

`NewInstanceClient` 复用同一显式应用的租户令牌管理。`CreateInstance` 需要调用方先保存 UUID 和来源版本映射，客户端不生成随机重试 UUID、不做应用层重发；`GetInstance` 支持原 UUID 或实例 Code。`60012` 为 `ErrUUIDConflict`，意味着必须查询原 UUID。失败只暴露安全操作名、HTTP 状态及数值错误码，不把响应体写入错误消息。

`InstanceRequest.Form` 在 Go 中是 JSON 数组，向官方 SDK 发送时转换为 JSON 字符串。当前禁止原实例内重提及按原表单再次创建，以免在本地来源版本映射之外产生新提交。该限制不等于已经支持撤销、退回重提或业务冻结。

`BuildDetailForm` 按显式 `DetailFormBinding` 将多条业务值映射为一个 `fieldList`：可选择系统 `id` 或唯一 `custom_id`，输出始终使用实时定义的系统 ID。按定义顺序输出，金额通过精确十进制写为 JSON 数字；币种显式提供，不猜默认值。联系人必须是审批应用的 open ID；附件必须是审批上传接口的 file code，不能拿 Base 文件 token 替代。

支持基础文本、日期、金额/数字、联系人、附件及单层明细。缺必填项、空文本、歧义/重复控件、旧模板 ID、不兼容的值类型及未绑定数据会阻止本地准备；复杂控件和条件需独立实现。`ValidateTemplateForm` 是发送前必须执行的当前模板校验；传输客户端的通用请求校验不代替它。货币允许范围等模板业务限制仍由实际模板和正式接口校验。

已通过隔离 HTTP 测试及真实旧企业模板读取、本地两行表单映射；未创建真实实例或上传文件，不能据此宣称完整人工审批闭环完成。阶段记录见[人工审批接口记录](../../../docs/progress/2026-10-05-native-approval-client.md)。

2026-09-30 已通过企业应用读取「海外易商卡」的官方定义：`approval_code=9944A2AE-ED45-43F3-9B87-0F3902F09844`，状态 `ACTIVE`，一个 `fieldList` 明细及 14 个子控件。完整原始响应与字段索引见 [`external-api/feishu/`](../../../external-api/feishu/README.md)。旧业务资料中的 5 字段映射不能直接用于当前模板。

## 共用业务适配

共用用例使用 `NewInstanceGateway`：按实时模板表单、节点元数据及本地绑定/自选审批人生成配置摘要，准备/创建前再次核对，按业务值类型严格映射控件。发起人自选节点缺人、未知 node ID、单选节点传多人及非 open ID 在本地停止。构造输入复制并排序审批人列表，后续调用方改 map 不改变已准备配置。

`NewInstanceLookupGateway` 只使用原应用的实例客户端和已存计划，不需要当前模板或表单配置，也没有建单方法；共用 `NewReconciler` 仅依赖查询接口。查询校验 UUID、模板、应用范围和发起人，支持终态观察，不把新模板配置用于旧实例。更多约束及测试见[gateway 记录](../../../docs/progress/2026-10-05-approval-gateway.md)。

## 审批附件准备

`NewFileUploader` 使用官方旧版 multipart 上传地址及显式目标应用凭证，拒绝跳转，不重发 POST；文件用途由控件决定，附件控件使用 attachment，即使原件是图片。逐项核对实际内容哈希、长度、MIME、文件名扩展和目标身份，成功仅返回带应用 scope 的 file code，忽略临时下载 URL。官方附件/图片上限为 50M/10M；本项目当前读取并核对的票据原件仍限 20MiB，不扩展既有识别入口的文件类型。

`ValidateUploadDraft` 用实时模板检查各行非附件值及上传用途。只在本地元数据副本中延后确有待上传原件的附件必填校验，逐行确保其他必填附件没有遗漏；不生成占位 file code、不改实际模板，也不产生可执行实例请求。文件准备完成后仍由 `ValidatePlan` 校验完整表单。

上传请求与历次状态位于中立 `core/approval`，持久化与复用由 `app/approval.UploadManager` 和 `state.Files` 承担。`prepare-approval-files`/`retry-approval-files` 只做显式附件准备，预览只读取已存上传凭据。本阶段隔离测试已通过，未真实上传或建单；完整配置、状态及恢复边界见[附件准备说明](../../../docs/approval-configuration.md#审批附件准备)和[过程记录](../../../docs/progress/2026-10-05-approval-files.md)。

## 完整实例请求审计

`InstanceRequest.WireBody` 与 `CreateInstance` 使用同一个 SDK body 构建器，保存真实 `form` 字符串、原 UUID、节点审批人及关闭的重提开关，不保存鉴权信息。`InstanceGateway.PrepareRequest` 只生成 `feishu.instance-create.v4` 不透明 JSON；核心只保存格式/字节/摘要，不解释平台控件。

`CreatePrepared` 在发送前重新读取当前模板，按已存计划生成实际请求，并与审计逐字节摘要核对；任何变化作为未发送的拒绝停止。`app/approval.AuditedGateway` 绑定一份具体已存计划/请求，保留共用 Submit 的来源重核、原子成员预约和一次发送/UUID 恢复语义。隔离 HTTP 测试核对完整 SDK body 与审计一致及响应丢失后不重发；不表示真实实例已经创建。

`prepare-approval` 保存整个显式选择的私有审计批次，尚无真实建单命令或人工结果交付装配。详见[完整请求准备](../../../docs/approval-configuration.md#完整请求准备)和[过程记录](../../../docs/progress/2026-10-05-approval-requests.md)。

后续已接 `app/approval.BatchService` 及显式 `submit-approval`：整批原子预约与审计关联、逐组发送意图、实际 SDK body 核对、部分失败和原 UUID 恢复已有联测。`check-approval` 使用原身份的 `InstanceLookupGateway`，不读取当前模板、不重建；本地状态/未发送预约放弃独立于平台客户端。企业配置及监督服务未更新，未真实创建或通知，人工结果交付尚待实施；命令及验收见[建单说明](../../../docs/approval-configuration.md#显式建单与恢复)和[阶段记录](../../../docs/progress/2026-10-05-approval-submit.md)。

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
