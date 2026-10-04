# 2026-10-05：审核诊断与结果交付恢复

承接来源联动阶段，继续 T4 的任务恢复。用户授权按计划持续推进，并要求清晰的本地提交与过程文件。外部主线仍是 Anyreceipt 识别及 SealAI 审核，两个自有能力独立配置切换。

## 问题与实现

原状态只显示 `unknown/failed/completed` 和 `delivered`，无法判断错误发生在提交、核对来源还是飞书写入。查看通常需要直接打开含票据事实及提供方 URL 的状态 JSON。结果补交付还构造审核客户端；切换提供方、缺少旧密钥或排除模型 SDK 后，恢复已保存结果可能被无关条件阻止。

- 新增只读诊断，不接受审核器或写入接口。本地查看只打开已有状态目录，不创建目录、锁或任务。线上核对只使用项目应用身份读取实际附件、台账、上下文及配置的流水；不调用 OCR、Seal 或模型。
- 同一明细的历史版本分别比较，复用同一提供方版本与规则版本下的当前准备。`delivered=true` 仅表示历史结果曾交付，不能替代当前事实有效性。
- 单独构造结果交付服务，接收回调、本地导入及补回写不装配审核客户端。新自动送审仍构造配置选择的提供方，保持真实能力要求。
- 保存安全失败分类、发生阶段、时间、HTTP 状态及平台数值错误码。错误字符串、响应体、凭证、票据事实和 URL 不进入诊断输出；原任务快照与版本算法不改变，旧状态兼容读取。
- 来源已删除、附件已清空、范围不符或事实版本变化时，保存 `delivery.state=superseded`，退出后台反复读取。归档状态保存失败仍返回可恢复错误，不能让回调提前确认。
- `check_failed/write_failed` 保留已收到结果并继续补交付。显式重试可以重新核对归档版本，业务事实精确恢复后交付原结果。已有 `delivered=true` 的重试为空操作；跨来源记录即使已经交付也拒绝显式操作。

## 使用与判断

本机包装器显式加载 `.env` 与调试参数，程序自身不加载 `.env`：

```bash
python3 deploy/run-local-debug.py review-status all
python3 deploy/run-local-debug.py review-status <明细记录ID>
python3 deploy/run-local-debug.py check-review <明细记录ID>
python3 deploy/run-local-debug.py retry-writeback <document-id>
```

前两项不访问外部系统；`check-review all` 可核对当前配置范围的全部历史版本。指定没有审核快照的明细时，返回首次准备的 `readiness`，不创建审核任务。待处理来源收件记录按整个业务范围列出，因为其受影响明细可能尚未解析。

| 状态或建议 | 含义与处理 |
| --- | --- |
| `not_checked` | 仅读取持久化状态；需要 `check-review` 才能判断当前有效性 |
| `current` / `changed` | 当前事实与此历史版本相同 / 不同；变化后按触发配置或显式提交新版本 |
| `source_removed` / `no_attachments` | 来源不再具备原审核条件；保留历史结果，不写入 |
| `unavailable` | 当前读取或准备失败；先排查权限、台账与资料 |
| `ledger_incomplete` / `ledger_conflict` | 对应附件的 OCR 台账未齐或缺有效内容 / 同一来源键命中多行 |
| `reconcile_provider` | 提交中或响应未知；先在提供方核对真实状态，不盲目重复发送 |
| `resolve_rejection` | 提供方明确拒绝；解决原因与协议冲突，当前无自动解除尝试入口 |
| `retry_writeback` | 已保存结果且未交付；显式恢复仅做来源核对与飞书写入 |
| `configure_writeback` | 已有结果但没有配置专用 AI 回写字段 |

明确的 Seal 4xx 拒绝仍排除 408/429；保留原错误类型以获取安全 HTTP 状态。网络超时或响应丢失继续保持 `unknown`。人工在 Seal 页面确认并取得真实结果后，可沿用 `apply-seal-result <已核验回调文件>` 导入已知任务；本阶段没有凭空增加供应商状态查询接口，也不通过删除状态或改 ID 绕过拒绝。

## 验证

`go test ./...`、`go vet ./...`、`go test -tags no_anthropic ./...`、`go vet -tags no_anthropic ./...` 及默认/外部主线构建通过；审核用例与服务装配的 `-race` 检查通过。

针对关键恢复路径验证：安全错误字段、不暴露私有事实、本地读取不产生文件写入、多历史版本各自核对且不重复准备、旧来源读取隔离、无快照准备、响应未知仍须对账、来源核对失败到写入失败再到成功、恢复不重复付费、已交付幂等、归档保存失败不确认、归档退出后台、精确恢复版本可显式交付。

企业副本只读核验使用外部主线二进制，并移除 Seal 提交 URL/Bearer 和自有审核模型/规则配置：

- 当前来源读取 2 份历史审核状态，两份均已交付；当前完整业务输入核对均为 `changed`，建议 `submit_current_revision`。
- 使用隔离的只读配置暂不加入支付证据，并固定各历史提供方/规则版本后，`reczz28JNpKh1Mk4` 与原快照相同，说明变化来自新增支付输入；`reczz28JGJx5y6y8` 仍变化，唯一可见输入差异是发票的查重候选。后来的同票号候选会使此前已交付结论过时，后续人工审批不能仅判断 AI 列是否有值。运行配置未改。
- 原有另一条明细首次准备为 `unavailable/ledger_incomplete`，准确区分“尚未齐备”与已审核。
- 本次诊断正式 8 个状态 JSON 完全不变；台账仍 7 行、Anyreceipt 余额仍 206。没有新 OCR、审核、模型调用或线上写入。

私有报告为忽略的 `data/public-debug/review-recovery-validation.json`，只读验证程序保留于 `tmp/_review-recovery/`，不提交凭证及完整状态。后续运行更新的实际版本与验证另行追加。

实现提交：`d9c2bd2 feat: inspect review versions and recover saved result delivery`。

## 运行更新

本机外部主线更新为 `public-debug-20261005-review-recovery`，仅重启 `com.yingqing.twc-approval-debug`。本地与公网的健康、就绪及版本接口返回预期；新增日志确认 WebSocket ready 与实际连接成功。两个修改触发开关仍关闭，10 秒编辑等待配置及原识别完成自动送审保留。

正式状态仍 8 个 JSON：请求、审核状态、结果、交付事实及其他任务内容均未变化；仅 1 个旧租户结果新增 `superseded/source_mismatch` 归档元数据，退出后台补交付。不能将本次部署描述为全部文件字节未变。台账仍 7 行、Anyreceipt 余额仍 206。

部署后的 `review-status all` 在移除审核提供方配置后成功读取状态；对已经交付的结果执行 `retry-writeback` 幂等成功，没有新审核、识别或线上写入。此项只验证已交付空操作，实际失败到成功的恢复与归档后精确版本恢复由前述隔离测试验证，未伪称新增真实回写样本。部署证据为忽略的 `review-recovery-runtime-{baseline,verification}.json`，私有文件权限为 0600。

## 后续边界

诊断不是供应商对账查询，也不自动重发明确拒绝的请求。暂未新增真实付费审核样本；来源联动开关继续关闭，原识别完成自动送审保留。读取与写入仍有跨系统并发窗口，业务冻结、停机修改补偿、飞书分组审批、人工结果、历史占用及结算继续按计划推进。
