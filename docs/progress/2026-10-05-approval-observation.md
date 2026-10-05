# 原生审批事件与状态观察

日期：2026-10-05。接续 `03936cc` 的明确失败重试；本阶段属于 T6 人工审批闭环的状态观察部分。实现事件持久化、真实查询和服务装配，不代表人工结果已经交付到 Base/Seal 或完成财务流程。

## 业务变化

配置 `approval.observation.enabled=true` 后，服务监听所选应用的原生 `approval_instance` 事件。收到通知先保存查询意图，再按已存创建 UUID 查询真实实例，核对原应用、模板、发起人及通知的实例 Code，保存 verified 状态与追加历史后确认意图。事件状态、时间、原始 JSON 和 token 不作为决策或存入状态。

查询/保存失败保留 pending；重复投递不会重新打开已确认意图。重启和定时查询补查漏投、停机变化以及 finished 后的撤销。创建关闭后仍可观察已建单，不依赖当前表单/AI/来源值，也不调用创建或审核 API。reserved、本地 abandoned/not_sent 和另一应用的尝试不查询。缺 UUID 的事件只匹配已确认实例，尚未知的关联由轮询按原 UUID 补查。

原生 GET 的 APPROVED + reverted=true 映射为中立 reverted；PENDING + reverted=true 拒绝。事件里的超时关闭/恢复只触发实际查询，不编造 GET 状态。批准/拒绝/撤销/删除均保留财务占用，终态不回退 pending。

## 架构与接线

- `core/approval.Notice` 只含持久化查询关联和安全诊断。
- `app/approval.Observer` 使用状态/查询端口；原业务事实、模板、创建和 AI 端口不参与历史观察。
- `state.Files` 在原来源 registry 中绑定意图与计划，用唯一目标/事件组合哈希去重；实例保存和意图确认之间崩溃只需再查询。旧无 notices 的 registry 继续可读，损坏关联停止。
- 共用 SDK 连接从 `feishu/base/events` 移至 `feishu/events`，保留原代码注释及 Base 筛选目录，通过 Event/Sink 别名消费共用契约。原生审批接入不归入 Base 业务域。
- `cmd/server` 为同应用注册 Base/审批两种事件，独立审批应用单独连接；Base 仅轮询时也能监听审批。审批不会进入 Base 原始日志。
- 观察 worker 以来源/应用领取 OS 锁，凭证组必须明确且唯一；关闭观察不构造客户端、worker 或新状态。

配置/恢复步骤见[审批配置](../approval-configuration.md#原生审批结果监听)；[示例片段](../../configs/approval/observation.fragment.example.json)需合并进完整业务文件。默认不开启，poll_interval 默认 5m、范围 1m～24h。仅当前 Base/Table 和所选应用下计划被观察；其他历史来源仍可显式 check-approval。

## 原始协议与订阅

使用 `lark-openapi-explorer` 按官方 llms.txt → llms-approval.txt 取得三份 Markdown 原文，按字节保存于 external-api/feishu，来源及 SHA-256 写入该目录 README 和总索引。

原生事件为 1.0 格式，顶层 uuid 是事件 ID，event.uuid 是创建 UUID；官方例子支持 SDK 长连接，REVERTED 的 operate_time 是整数。它不同于人员/任务范围的 v4 状态事件。后台注册/事件权限之外，还需订阅每个审批定义。

新增 `subscribe-approval-events <配置中的template_code>`，仅明确启用观察且参数匹配时，使用所选应用先读取定义、再发送一次 SDK Subscribe。启动不订阅。HTTP/code 错误保留安全分类；1390007 同时指已订阅或已取消，因此不是确认有效。成功输出 accepted 和 event_delivery_verified=false，不能当作实时投递验收。

## 验证结果

通过：

```text
go test ./...
go vet ./...
go test -tags no_anthropic ./...
go vet -tags no_anthropic ./...
go build -o tmp/approval-preflight/twc-approval-default ./cmd/server
go build -tags no_anthropic -o tmp/approval-preflight/twc-approval ./cmd/server
go test -race ./cmd/server ./internal/app/approval ./internal/core/approval ./internal/config ./internal/feishu/approval ./internal/feishu/events ./internal/feishu/base/events ./internal/state
git diff --cached --check -- . ':(exclude)external-api/feishu/approval-event-overview.md' ':(exclude)external-api/feishu/approval-instance-event.md' ':(exclude)external-api/feishu/subscribe-approval-events.md'
```

隔离验证覆盖 SDK P1/P2 同连接分发、两种 UUID 分离、撤销整数时间、应用隔离、保存失败不确认、建单响应丢失时预先入队、查询事实覆盖事件暗示、查询/确认保存失败后重启、12 个并发重复事件、同 ID 重定向拒绝、实际 Code 不一致、损坏持久化关联、无 UUID 映射、终态后取消/撤销、财务占用保留、未发送跳过、单 worker 锁和关闭创建后的独立装配。实际 SDK HTTP 测试验证原 UUID GET/reverted 及单次 Subscribe/1390007/协议错误；不使用真实写接口。

初轮新增接收器测试有一项把另一个事件类型当作“本类型坏载荷”；修正为显式传入本类型后验证 envelope 拒绝，真实 SDK 分发仍验证按类型路由。后续完整检查及竞态均通过。

暂存后全量 whitespace 检查发现供应商三份 Markdown 原有行尾空白/末尾空行；为保持接口原文字节与 SHA-256 不改写，只在 whitespace 检查中显式排除这三份原文，其余源码/配置/说明检查通过。原文重新核对三个哈希一致。

新 no_anthropic 二进制在真实企业配置下检查六个命令保护（包括新增订阅命令），全部预期停止。应用身份只读三行预检仍为一份 no_review、两份 review_stale，creation_available=false。17 个状态文件逐字节哈希不变，选中的 enterprise-test.json 和监督运行二进制哈希不变。私有验收为 data/public-debug/approval-observation-guard-validation.json，0600，未提交。

未真实订阅模板、上传、建单、审核或发送通知；未开企业观察配置、未替换监督服务。当前实际运行版仍为 public-debug-20261005-review-recovery，既有 Anyreceipt/Seal 与自动触发照常运行。隔离事件测试不能替代真实审批长连接验收。

## 剩余工作

1. 查询并保存真实审批人、流程时间线、评论和完成时间的中立结果快照；区分批准、拒绝、撤回与后续撤销，不从事件凑造身份。
2. Base 人工结果与 Seal manual-result 分别建立版本关联和可恢复交付状态；任何一侧失败不重复送审或掩盖另一侧成功。
3. 核对企业模板/人员权限和真实字段后启用配置，进行原应用下真实建单、状态事件、重复投递和人工交付验收。
4. 单主机 registry 当前保存全部历史意图并逐条轮询；规模扩展还需状态清理、限流/分页、多来源观察。财务冻结、释放、分摊、结算和高级权限仍按原计划继续。
