# 明确失败的原请求重试与未发送证明

2026-10-05，承接 `98b1571` 的审计建单与 UUID 恢复，继续 T6。本阶段修复“权限恢复后，同 UUID 的明确失败仍无法再提交”以及“后续查询错误覆盖原拒绝证据”的恢复缺口。企业配置和监督服务仍未启用原生审批；完整人工、财务流程目标继续。

## 恢复协议

新增 `retry-approval <preparation_id>`，只消费已经保存且已有尝试的原审计批次。使用当前显式 manual 配置，来源/目标应用、全部当前 AI 证明、业务值、上传凭据及实时 SDK body 必须再次与原批次一致。完全未提交的批次返回 not_submitted，首次建单仍用 submit-approval。

普通 submit 不自动重试 failed。显式 retry 只重开有原始明确拒绝、本地放弃或同调用方未发送证明的组；submitting/unknown、未找到、查询无权限、UUID 冲突不能证明没有建单。已经确认的组复用原映射，其余 reserved 继续正常领取。没有把实际人工审批的 REJECTED/撤销/删除当作建单失败；这些已创建实例保持财务预约。

重试保持 Plan、原 UUID、AuditReference、preparation ID 和请求 SHA-256 不变。新增 run（初次为 0）、run_started_at、closed_runs 及 no_creation。每次重开先在原来源 registry 的同一原子替换中追加已结束的轮次，保留原始证明和最后查询诊断，再清空当前错误、进入 reserved。完整表单仍只在原 0600 私有审计中。普通 Update 不能更改轮次、发送标记、已结束记录、原证明或审计。

重试前观察的每组 run 传入原子事务比较；旧观察不能在后一轮又失败后再次重开它。释放后若成员被另一计划取得，整个重试停止，不部分重置其他组。12 个并发文件句柄对同一观察只重开一次、领取一次。创建结果及 UUID 查询更新也核对观察时的 run/发送标记；上一轮迟到的结果不能写到下一轮。

no_creation 独立于最新 failure。明确拒绝后查 UUID 又失败，只更新诊断，原拒绝依旧可追溯、可用于显式重试。查询真的确认了原实例，则实例映射优先，继续保留早先证据但不再允许重发该组。旧状态缺新属性按 run=0 读取；只有原带时间的 creation/failed 或 preparation/abandoned 仍完整、且时间落在该轮范围时才能兼容重试。旧状态只剩 failed/lookup_failed 时不补造原证据，早期没有 audit 的尝试也不改绑。

## 发送意图保存报错

领取前由调用方生成随机私有发送标记，随 submitting 一起持久化。若领取返回错误，程序确定尚未调用建单 gateway，可以尝试记录未发送证明；状态事务必须同时匹配来源、计划、audit、run、标记，且仍为 submitting、没有已确认实例。匹配后保存 failed/persistence/not_sent，供后续显式 retry。原 HTTP 发送一旦调用，就没有这一分支。

领取前保存失败、别人的标记、上一轮标记、unknown 或已确认实例均不能用此证明释放。证明本身保存失败时继续保留 submitting，仍需原 UUID 对账；重启或运维不能依据“没有响应”人工补这个证明。没有开放接收标记的 CLI 修复命令。实际远端接受后的结果保存失败仍保留不确定性，不补“未发送”。

本地 status 输出 run、closed_runs、安全 no_creation 和 retryable；不输出私有发送标记、表单、原件或上游报错正文。retryable 只表示存在恢复证据，不能替代实时来源检查或成员预约。未发送证明、本地放弃及 reserved 不执行远端查询；明确拒绝的原 UUID 仍可显式查询。

## 接口与验证

重新下载[飞书官方创建审批实例说明](https://open.feishu.cn/document/server-docs/approval-v4/instance/create.md)，与已归档的 `external-api/feishu/create-approval-instance.md` SHA-256 一致（`f32fdf3226bcac78302adb491876b2747a8b9b1c8d528c922a12c8aa882512d6`），没有改写原文。UUID 冲突协议保持原实现，不假定明确失败后平台一定接受相同 UUID；若重试返回 60012，当前轮进入 unknown，只查询原 UUID。

隔离官方 SDK HTTP 测试覆盖 400/99991672 明确拒绝、同 body/UUID 再发成功以及再发返回 60012 后查询、仅恢复未发送组。POST 必须在新轮次意图保存后发生，实际 SDK body 与第一次请求逐字节一致。测试使用本地传输及假实例/文件 code，不代表企业真实建单验收。

业务与持久化测试覆盖查询失败不丢原证据、已确认查询压过早先拒绝、已知实例重复 retry 零来源读取/零建单、旧轮查询/创建结果隔离、成员重新占用、AI 变化、另一批审计及旧证据缺失时整批停止；本地放弃可恢复。发送意图保存前、保存后、证明保存失败和远端接受后保存失败分别测试，确实未调用 RPC 的保存后错误可通过显式 retry 恢复。旧/错轮次、audit、标记及已知/未知实例均不接受未发送证明。

以下检查全部通过，日志与临时构建位于忽略的 `tmp/approval-preflight/`：

```text
go test ./...
go test -tags no_anthropic ./...
go vet ./...
go vet -tags no_anthropic ./...
go build ./cmd/server（默认及 no_anthropic）
go test -race ./cmd/server ./internal/app/approval ./internal/core/approval ./internal/config ./internal/core/review ./internal/feishu/approval ./internal/feishu/base ./internal/state
git diff --check
```

新 no_anthropic 二进制使用本机当前企业环境验证：

| 检查 | 结果 |
| --- | --- |
| submit-approval、retry-approval | 未配置 manual，在状态/网络装配前拒绝，exit=1 |
| approval-status、check-approval、abandon-approval 未知审计 ID | exit=1，不创建状态文件或调用实例 API |
| preview-approval 真实三行 | 一份 no_review、两份 review_stale；approval_not_configured、creation_available=false |
| 私有状态目录 | 17 个文件 SHA-256 全部未变 |
| 选中企业配置及监督二进制 | 字节未变，未部署更新 |

证据 `data/public-debug/approval-retry-guard-validation.json` 和 `approval-preflight-validation.json` 均为 0600，不提交 Git。此次真实操作仅使用应用身份只读来源；没有 OCR/审核方/上传/实例创建/实例查询/通知。未启用 manual 的命令保护及三行预检不替代完整配置下的真实企业建单、人工流程验收。

## 剩余范围

本阶段只重试原批次，不改绑另一批 preparation ID。部分创建后若已知组来源变旧，原整批检查仍会停止其余发送；拆分/重新准备剩余组需要保留历次审计关联的独立协议，不能靠改 UUID 或删除 registry 解锁。

继续人工事件触发的真实实例观察，以及分别持久化、独立恢复的 Base 人工结果和 Seal manual-result 交付。真实目标企业身份、完整当前 AI/来源/表单仍需配置验收；人员或模板不可用时不回退旧应用、不填虚构值。财务占用/分摊/结算、远端撤回和退回重提、权限冻结、T4 停机修改补偿、T5 PDF/多页仍未完成。
