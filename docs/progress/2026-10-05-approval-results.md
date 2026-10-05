# 人工审批结果证据

日期：2026-10-05。接续 `b08e4a1`，继续 T6；本阶段将仅有实例状态的观察扩展为真实查询的流程证据，作为后续独立交付依据。当前没有 Base/Seal 人工结果发送，不等于完整财务流程验收。

## 业务与架构

- 新增中立 ResultSnapshot v1，记录 verified 实例、任务/原应用 actor、开始/完成毫秒时间、评论、流程动作、关联人员、修改/撤销原实例 Code 和附件名称/类别/实际大小。保留缺失与明确 0，临时访问 URL 不存；只用 open_id 作为原应用身份，user_id 不补成 actor。原生业务表单不复制进这个流程快照。
- Native InstanceLookupGateway 的同一次原 UUID GET 同时提供状态和完整可得流程证据；SDK DTO 留在 Feishu 适配层。可选 ResultLookupGateway 保持已有状态端口兼容，AuditedGateway 透传 richer lookup，旧端口明确记录 status_only，不能被当作人工证据。
- 结果按实际内容生成 SHA-256，任务/评论/动作排序及 JSON 元数据键顺序不影响版本；元数据中大整数保持精确。构造器冻结调用方指针/切片，每份最大 8MiB。当前保存于原单来源 registry；数据量增长后需要独立内容存储/索引演进，不能将当前文件库当多主机方案。
- 查询成功保存结果及追加版本历史；状态相同但评论等变化仍产生新版本，相同结果不重复保存。原状态 History 保留现有语义，Results 另保完整证据，ResultObservedAt 表明该证据也被最新查询核对。
- 状态/结果在一次 registry 更新中保存。历史只追加，不可改写/删除，晚到旧查询不能覆盖更新状态或证据。最新失败或只查状态时旧快照仍可审计，但 CurrentResult 不将其作为当前证据；下一次完整成功读取恢复当前性。
- 原有事件确认与重启恢复复用新查询；无需当前模板/Source/AI，也不创建或重新审核。财务占用仍保留。

## 人工归属与诊断

HumanDecision 只从 verified approved/rejected、真实完成时间和唯一最后完成任务提取。任务必须与实例终态一致，属于已知人工审批方式并带原应用 actor；同一时间多个候选不按排序选一个，自动动作不冒充人工。未完成/缺少时间、未知任务状态/流程动作、人员缺失等均给安全问题码。

驳回理由只来自该最终任务和同一人员的拒绝动态，选最新可得时间；同一时间不同理由报歧义。普通评论、另一个人的说明不填成理由；缺理由保留空。撤回、删除、撤销不转换为 Seal reject。姓名和邮箱仍需要原应用下身份解析；最终任务可归属不代表原生表单和原审计值一致或当前业务事实/财务决定已验证。

approval-status/check-approval 增加结果 revision、任务/评论/动作数量及 human_result_issue，私有文本/附件/人员不输出。具体配置/边界见[结果说明](../approval-configuration.md#人工结果证据快照)。

## 验证

以下均通过：

```text
go test ./...
go vet ./...
go test -tags no_anthropic ./...
go vet -tags no_anthropic ./...
go build -o tmp/approval-preflight/twc-approval-default ./cmd/server
go build -tags no_anthropic -o tmp/approval-preflight/twc-approval ./cmd/server
go test -race ./cmd/server ./internal/app/approval ./internal/core/approval ./internal/feishu/approval ./internal/state
git diff --check
```

覆盖内容版本/冻结/排序/精确 JSON 数值、未知标签、缺失字段、8MiB 限制、唯一/自动/并发候选人工归属、只取真实任务拒绝理由、重复轮询与同状态元数据变更、不可变历史、重启、较旧并发查询、最新失败或状态端口后的证据失效、事件 Code 冲突、命令安全摘要。实际 SDK HTTP 隔离联测验证 GET 一次取得任务/评论/时间线/关联实例，保留附件大小 9007199254740993 与缺失大小，临时 URL 不存、user_id 不替代原应用 actor；没有真实人工操作。

最初编译检查发现 SourceMetadata 字段放入了 Comment 而非 Action，已在执行测试前修正；后续相关和完整测试、vet/构建/竞态均通过。

新 no_anthropic 二进制在真实企业配置下通过六个命令保护及三行应用身份只读预检，仍为一份 no_review、两份 review_stale，creation_available=false。17 个状态文件逐字节不变，enterprise-test.json 与监督二进制哈希不变；公网 /version 仍为 public-debug-20261005-review-recovery。私有结果为 data/public-debug/approval-result-guard-validation.json（0600、未提交）。

本阶段未启用观察、未真实订阅/上传/建单/读取原生实例/审核/通知或人工交付。旧结果状态无 Results 字段仍可读，既有 Anyreceipt/Seal 自动主线不变。

## 下一步

1. 核对原应用 actor 的姓名/邮箱与真正完成时间，保存明确身份来源；不凭发起人/名单排序/显示名猜最终审批人。并行同时间多候选保持需要明确业务口径，Seal 单个 approver 协议不能自行承载多个人。
2. 交付前核对原审计、原生表单变化和当前 Base 版本关系，避免把原实例批准解释为已冻结所有远端事实。
3. Base 人工字段与 Seal manual-result 分别有持久化意图和交付状态，按结果版本/来源/目标/原审核映射固定，不因一侧失败再审核。Seal 只接受 approve/reject，不能伪造撤销或未知发送结果的查询/幂等协议。
4. 真实模板/人员/字段核对后进行创建、事件、重复投递和部分交付验收；财务冻结、分摊、释放、结算及高级权限按原计划继续。
