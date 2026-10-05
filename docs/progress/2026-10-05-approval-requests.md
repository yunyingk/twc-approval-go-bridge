# 完整审批请求审计批次

2026-10-05，承接 `4f3e992` 的原生审批附件准备。本阶段补齐建单前可审阅的完整请求，继续 T6：整组来源、当前 AI 证据和实际 SDK 请求一起持久化，后续具体提交可据此 review。企业配置及监督二进制未更新，没有真实上传、建单、通知或付费审核。

## 问题与实施选择

先前预览只给计划摘要，来源值和已上传文件引用可以临时组成完整表单，但没有保存当次审核证据与实际创建 body。仅 review 一个 UUID 或本地表单数组，无法确认发起人、节点审批人、精确金额、附件引用和重提开关最终如何进入 SDK 请求。

新增 `prepare-approval <记录ID[,记录ID...]>`；完整 manual 配置、选中的全部行、完整必填表单及已确认附件都必须可用。它不隐式上传、不重跑审核、不领取成员、不创建实例。将首次准备所得整组计划/请求与第二次重新读取的整组结果比较；任一来源、AI 结果、模板或请求变化停止整个批次。API 多次读取只能检测观测到的变化，不是 Base 事务冻结。

边界继续保持：

- `core/approval` 定义不透明 RequestArtifact、已核对 ReviewProof 和完整 PreparedBatch；无 SDK/平台控件逻辑。请求保留精确 JSON 数字与字符串，不经过 float64。
- `app/approval.RequestPreparer` 依赖只读来源、请求生成器和私有状态接口；所有组通过且两次内容一致才保存一次整批。字段缺失、过期 AI、缺上传凭据、最后一组失败均不能留下可用子集。
- `feishu/approval` 的 WireBody 与实际 CreateInstance 共用 SDK body 构建器。保存的是正式创建请求：form 为 JSON 字符串，含 UUID、发起人/可选部门、排序后的节点审批人、allow_resubmit=false、allow_submit_again=false；鉴权头不在载荷中。
- `state.Files` 使用独立版本化 namespace、OS 锁和原子写保存批次，0600 文件。重复摘要保留最初 PreparedAt；已存空/损坏/错误版本文件停止而不覆盖。审计不进入 AI 交付扫描，不创建审批尝试或占用成员。
- `AuditedGateway` 固定已保存的一份计划/请求，接入既有 Submit 与恢复协议。发送前实时生成请求并与审计格式及 SHA-256 核对；失去响应仍保留 unknown 及原 UUID，不重发。

批次固定所有成员来源/目标/配置版本、分组、当前 AI revision 与结果、业务值、已上传 file code/provenance、完整原生 body 和请求摘要。审核证明保留实际核对的票据事实、OCR 原始响应、查重候选、支付证据与提供方/规则 pin；原件二进制/附件临时 URL 排除，另存每份内容 SHA-256 和实际长度。读取时间归零、各类列表稳定排序，避免同一事实因抓取时间/返回顺序产生新批次。

证明仍保存原始完整审核请求 revision；去掉二进制的审计 snapshot 不是可重新提交的 AI Request。批次摘要检查完整性，不是签名或权限凭证。新快照的本地完整性不代替创建前再次重核来源/模板。

stdout 只给安全阻塞原因、成员/计划/revision、preparation_id、请求摘要和私有审计路径。`preparation_complete=true` 表示整批确实保存成功，creation_available 始终 false；保存错误不输出成功报告。完整值、评论、file code、OCR 原文仅在私有审计内，不进 Git。使用方式见[完整请求准备](../approval-configuration.md#完整请求准备)。

## 恢复问题修复

发现审批 registry 解码将长度为零的文件视作缺失。已有状态文件被截断时，后续可能忘记原实例/成员预约并创建另一计划。改为只有真正不存在（nil）才初始化；已有空文件明确失败。回归测试先建 pending 实例、截断 registry、改变金额并再提交，必须阻塞且只发生原来的一个实例请求。审计和上传 namespace 同样遵循“损坏不等于缺失”。

## 验证

专项测试覆盖完整两组私有审计及默认 JSON 隐私、稳定摘要/原时间复用、12 个并发状态句柄、空/损坏审计不覆盖、原始证据不修改、精确金额与内容 SHA/长度、列表排序/临时 URL/读取时间不影响审计、事实/原件变化拒绝、最后一组失败及准备期间编辑不留子集、缺上传 code 不补传、请求/证明被修改不通过。保存前失败和原子保存后调用方仍看到错误，都不能报告完成或预约成员；后者重跑复用已经写入的审计。

隔离官方 SDK HTTP 测试同时验证：真实发送 body 与保存的请求逐字节一致；发送前 submitting 和所有成员预约已持久化；请求/格式/当前模板变化时零实例 POST；响应丢失后同一计划不重复发送。此测试使用本地假传输和仓库真实模板结构，不替代真实企业建单验收。

以下均通过：

```text
go test ./...
go test -tags no_anthropic ./...
go vet ./...
go vet -tags no_anthropic ./...
go build ./cmd/server（默认及 no_anthropic，输出在忽略的 tmp/）
go test -race ./cmd/server ./internal/app/approval ./internal/core/approval ./internal/core/review ./internal/feishu/approval ./internal/feishu/base ./internal/state
git diff --check
```

新 no_anthropic 二进制使用当前企业应用身份真实只读复核：

| 检查 | 结果 |
| --- | --- |
| preview-approval 三行 | 两份 review_stale、一份 no_review；approval_not_configured，creation_available=false |
| prepare-approval 当前未配置入口 | exit=1，在状态/网络装配前拒绝，没有准备/上传/建单 |
| 状态目录前后 | 17 个文件哈希全部不变 |
| 选中的 enterprise-test.json 与监督二进制 | 字节哈希不变，没有滚动运行服务 |

私有证据 `data/public-debug/approval-request-preparation-validation.json`、`approval-preflight-validation.json` 为 0600，不进 Git；临时验证脚本和默认/外部构建在忽略的 tmp。真实复核不调用 OCR/审核方/审批上传/实例/通知；本地完整请求测试通过不代表企业样本已满足建单前置条件。

## 后续

继续把已审阅批次和请求摘要关联到建单尝试，接显式提交与原 UUID 状态/恢复命令，再接人工事件、Base/Seal 独立交付及版本冲突。当前实际配置没有目标审批契约，两个已交付 AI 样本与当前事实不一致、演示行缺交易/项目，旧模板对桥接应用不可读；不以假值或跨应用 open ID 补齐。

财务释放、占用/分摊/结算、撤回重提与权限冻结仍是独立业务工作；T4 停机修改补偿和真正冻结、T5 模型 PDF/多页及提供方组合评估也继续有效。本阶段不宣称完整业务完成。
