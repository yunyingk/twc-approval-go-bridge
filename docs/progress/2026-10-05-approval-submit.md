# 显式原生建单、审计关联与 UUID 恢复

2026-10-05，承接 `8e6361a` 的完整私有请求审计。本阶段将具体已审阅请求接入显式建单，并补本地状态、原身份 UUID 查询与未发送预约放弃，继续 T6。当前企业配置/监督服务未启用原生审批，没有真实上传、实例、通知或付费审核；完整人工/财务业务目标仍继续。

## 整批意图与部分失败

先前共用 Submit 只接单个分组，审计批次与建单尝试没有持久关联。直接循环各组会在后组发现占用/非法输入前已经创建前组，也无法区分“预留了但还没发”与“已发却丢响应”。这两种情况有不同的恢复行为。

`submit-approval <preparation_id>` 只消费已保存的完整审计，且要求当前显式 manual 来源/目标匹配。先重新准备整组选中行与所有请求（不保存新审计），原计划、证明和 body 摘要一致才进入来源 registry 的整批原子预约；后一组占用也不能部分预约前组。所有新组都保存 plan + audit，phase=reserved。

逐组再次核对完整 AI 证明、来源值及实时模板 body；将 reserved 原子转换为 submitting，只有领取者发送一次。继续使用既有创建响应、保存与未知恢复语义。分组外部创建不具有批次事务：后一组失败保留前组实例及后面的 reserved。首组响应未知时停止后续组，先查原 UUID，确认后重核并恢复剩余组。

`AuditReference` 固定 preparation ID、request format、request SHA-256；状态更新不能增删/修改它，不能把另一批审计或旧无 audit 的尝试改绑。原 UUID 保持计划身份。完整请求仍在原 0600 私有审计中，registry 保存不可改绑的关联；stdout 仅安全成员/状态/UUID/实例 Code/摘要和错误分类，不包含表单、原件、评论或上游文本。

## 命令与恢复边界

- `approval-status` 只读本地审计/registry，缺记录不创建文件/锁，不要应用凭证。all_instances_known 是历史映射观察，不能当作当前 AI、人工批准或结算完成。
- `check-approval` 不构造来源/审核方/模型，不读取当前模板或规则；按已存目标 app ID 在两个环境凭证组中匹配恰好一组。缺原密钥、未知或重复 ID 拒绝，不回退当前新应用。查询原 UUID，核对目标/模板/发起人，保存真实状态和安全失败分类。未开始、reserved 和本地 abandoned 不查询。
- `abandon-approval` 在同一 registry 锁下只把 reserved 记为 failed/preparation/abandoned。另一进程已领取则不能放弃；submitting、unknown、确认实例及原审计都保留。它不远端撤回、不释放已批准/撤销/删除的财务占用。
- 失败时尽可能输出可读的部分状态，再以非零退出结束；原始来源/提供方错误不进入日志。已经全部确认的相同批次只读返回原映射，无需当前事实/模板仍可读，也不会重新建单。

领取意图的原子写已发生但调用方收到错误时，可能实际尚未 POST，仍保留 submitting；当前不能据此宣称“未发”并重试。明确拒绝/本地放弃也不自动重试相同 UUID，权限修复后相同内容仍会停止；专门的明确失败重试及未发送证明修复协议待补。新的实际业务/配置版本才可形成另一计划，不能只换随机 UUID 避开未知。

来源及模板重核是观测检查，仍没有 Base 原子冻结。查询/人工终态不释放财务预约，部分外部创建也不能通过删除状态或本地 rollback 撤销。完整命令及 phase 表见[建单与恢复](../approval-configuration.md#显式建单与恢复)。

## 验证

专项覆盖全批审计/成员预约先于任何创建、精确 body 与 proof 隐私、已确认重复提交零来源读取/零创建、当前 AI/字段绑定变化、后一组占用、旧无 audit 尝试及损坏审计时整批停止；发送前/原子发送意图后/远端接受后保存失败不重发，取消后保留已接受映射及未发送组。

12 个并发文件状态句柄预约/领取只有一个发送者。首组丢响应时后组 reserved，查原 UUID 只查询首组，恢复仅发送剩余组；前组创建后后组来源变化也停止。放弃仅释放已证明未发送的组，原 unknown 继续占用，audit 不可修改。

隔离官方 SDK HTTP 联测使用真实模板结构和完整当前证明：POST body 与原私有审计逐字节一致、每次 POST 之前全部组已预约且当前 exact audit/submitting 已落盘；首组未知后，当前模板被改成不可解析仍能用 LookupGateway 查询原 UUID；恢复模板后只发送第二组。测试实例/文件 code 均为本地假传输，不能当作真实企业建单验收。

最后检查发现并修复：创建失败后的首次查询可能又失败，将 failure 阶段改成 reconciliation；下一次检查仍须继续原 UUID 查询，不能因为当前 failure 已不是 creation 就报告“未提交”。`Attempt.NotSent` 仅认可 reserved 或确切 preparation/abandoned，单个 Reconciler 也不查询这两类；CLI 回归测试核对先前查询失败后仍要求原凭证，不能无凭证直接返回 not_submitted。

以下均通过：

```text
go test ./...
go test -tags no_anthropic ./...
go vet ./...
go vet -tags no_anthropic ./...
go build ./cmd/server（默认及 no_anthropic，输出在忽略的 tmp/）
go test -race ./cmd/server ./internal/app/approval ./internal/core/approval ./internal/config ./internal/core/review ./internal/feishu/approval ./internal/feishu/base ./internal/state
git diff --check
```

新 no_anthropic 构建用当前企业环境验证命令保护与应用身份只读预检：

| 检查 | 结果 |
| --- | --- |
| submit-approval 当前未配置入口 | exit=1，在状态/网络装配前拒绝 |
| approval-status/check-approval/abandon-approval 未知审计 ID | exit=1，未创建状态文件/锁，未调用实例 API |
| preview-approval 真实三行 | 两份 review_stale、一份 no_review；approval_not_configured，creation_available=false |
| 状态目录、当前企业配置、监督二进制 | 17 个状态文件哈希全部未变；配置/原监督二进制字节未变 |

私有证据 `data/public-debug/approval-submission-guard-validation.json`、`approval-preflight-validation.json` 为 0600；临时二进制/验证脚本在忽略的 tmp。未调用 OCR/审核方/上传/真实实例创建或查询/通知。未知审计 ID 的保护验证只证明此场景的零副作用，不替代完整配置下真实建单/人工流程验收。

## 后续

继续补人工事件触发的真实状态观察、独立持久化的 Base 和 Seal manual-result 交付、旧版本冲突与重启补交付；补明确失败/未发送证明的恢复协议和正式真实建单验收。退回重提、远端撤回、占用/分摊/结算、权限冻结、T4 停机修改补偿与 T5 PDF/多页仍未完成。

企业配置未启用审批，现有两份 AI 已交付样本均 stale，另一个原样本无已存审核；演示缺交易/项目，旧模板对桥接应用不可读。实现这些可验证的编排不需要用假值、旧身份回退或额外付费审核填补真实业务契约。
