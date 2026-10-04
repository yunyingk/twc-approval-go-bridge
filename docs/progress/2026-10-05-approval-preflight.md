# 人工审批配置与当前 AI 结果预检

2026-10-05，承接 `81212db` 的原子成员预约与严格状态迁移。本阶段接入显式配置、真实审核来源和只读门禁，不宣称完整原生表单或人工流程已完成。

## 改动

- 选中业务文件可声明 `approval`：目标凭证组、模板/发起人、明确 AI 建议策略、分组轴/时区、来源字段、精确金额币种、人员映射和控件/节点绑定；无目标或分组默认值，无身份回退，凭证仍在环境。
- `app/approval.ReviewGate` 只依赖读取接口，按选中审核提供方检查已存状态和当前事实。共用 `review.PreparePinned` 沿用原 pin，读取实际内容而不重新审核；JSON 排除完整事实、二进制、评论、链接和 pin。
- 每个历史 revision 单独比对，同 pin 只准备一次；过期、在途、无结果、源数据错误和策略阻塞分别返回。多份仍当前的 pin 判冲突，不选“列表最后一份”或曾 delivered 的结论。
- `preview-approval` 支持明确多个记录，先验证 AI 门禁，再对完整 manual 配置只读查目标模板元数据。来源字段取值/审批上传/完整原生计划仍未接入，输出明确 creation_available=false 和 form_source_preparation_pending。

## 隔离与真实验证

测试覆盖精确当前事实与 JSON 隐私、金额/支付/查重候选变化、旧 completed 与当前 pending、当前多 pin 冲突、无策略或排除建议、来源删除/台账不齐/权限失败、跨来源及无效状态提前拒绝；配置测试覆盖身份/人员/分组/时区/完整绑定、已审核的流水前置、币种歧义、环境凭证选择及拒绝 JSON 密钥。命令测试确认无状态新建及目标凭证缺失不回退；没有模型密钥也可读取已有结果。

使用独立 `no_anthropic` 构建和现有三层私有运行环境，只读检查企业三行：

| 明细记录 | 结果 |
| --- | --- |
| reczz28JGJx5y6y8（EXP-DEMO-013） | review_stale |
| reczz28JNpKh1Mk4（EXP-DEMO-014） | review_stale |
| reczz28HBeFn4GdB | no_review |

选中企业文件未声明审批，配置结果为 approval_not_configured、form_source_preparation_pending。整个状态目录 17 个文件逐字哈希未变；没有识别、送审、上传、通知或创建调用。完整只读报告在私有 `data/public-debug/approval-preflight-validation.json`（0600），临时验证程序在忽略目录 `tmp/approval-preflight/`，不把敏感配置放入 Git。

默认/no_anthropic 全量测试、vet、构建及审批用例/审核用例/配置/命令 race 检查通过。当前业务 JSON 未修改，监督二进制/公网服务未更新。

## 继续推进

接下来按已明确的配置实现 typed Base 来源、稳定关系分组、时区分桶、目标人员映射及完整 plan 预览；保留严格 AI 门禁。真实目标模板、发起人/审批人和缺失业务字段还未配置，不能拿假身份或历史同名字段补齐。之后继续审批上传与复用、完整原生请求审计快照、受控建单、人工结果交付、Seal manual-result、撤回重提、占用/分摊/结算及权限。明确拒绝的原计划安全显式重试也仍是未完成项，完整目标保持不变。
