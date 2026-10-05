# 原生审批附件准备、持久化与复用

2026-10-05，承接 `ffde39b` 的类型化表单来源。本阶段实现独立审批上传、已存结果接回预览及显式准备/拒绝重试命令；当前企业配置仍没有 approval，监督服务未更新，没有真实上传、审批实例或通知。这是 T6 的一个阶段，完整业务目标继续有效。

## 协议发现

按飞书 `llms.txt` → `llms-approval.txt` → 文件目录获取[上传文件官方原文](https://open.feishu.cn/document/server-docs/approval-v4/file/upload-files.md)，跟随文档读取[通用错误码](https://open.feishu.cn/document/ukTMukTMukTM/ugjM14COyUjL4ITN.md)。CLI 没有封装此上传操作，适配使用显式目标应用的 tenant token 和原始 HTTP，不发起用户 OAuth。原始字节和校验值保存在 `external-api/feishu/`，供应商排版保留。

上传端点为 `https://www.feishu.cn/approval/openapi/v2/file/upload`；每次一个文件，multipart 的 name 必须带扩展名，type 按控件用途取 attachment/image，content 是原始文件。官方附件/图片上限为 50M/10M；本项目审核读取仍保留 20MiB 及图片/PDF 范围。当前 attachmentV2 使用 attachment，不能按图片 MIME 自动改用途。

返回 data.code 才是审批文件引用，Base token 不能替代；12 小时是 data.url 的有效期。官方没有给出 code TTL、上传幂等键或结果查询协议，不编造自动对账、按时失效重传或可跨应用/记录使用的通用缓存。

## 实施边界

- `core/approval.UploadRequest` 固定内容 SHA-256、来源应用/Base/Table/行/字段/token、目标应用、文件名、MIME、实际长度及用途。稳定身份覆盖这些元数据；二进制和下载 URL 不入状态。
- `state.Files` 独立 namespace 保存请求与历次 uploading/unknown/rejected/uploaded 尝试。跨本机进程通过 OS 锁和原子写保存发送前意图。请求、旧历史及确认引用不可改变；重试只能追加明确拒绝的新尝试。文件/锁为 0600，不进入 AI 回写任务扫描。
- `app/approval.UploadManager` 校验字节/目标后才领取；已有 uploaded 直接复用。未知响应、进程中断和成功后保存失败均保留原意图，不重发。只有 POST 未发生的失败或官方确认的拒绝才允许显式重试。Begin 在原子替换后失败时，在状态库恢复后保存“尚未调用”的证明；证明也保存失败则继续保持 uploading。
- `feishu/approval.FileUploader` 使用所选应用凭证；拒绝 HTTP 跳转，严格核对响应显式 code、HTTP 状态及 file code，不将供应商文本/URL写入错误。鉴权/权限/IP 拒绝需一致的 HTTP 响应；断连、内部错误、5xx、429、缺字段、非 JSON 等保持未知。
- `PreparedSource` 从当前 `ReviewGate` 已核对原件中匹配明细 token；同时核对元数据、实际字节和保存的上传请求。未进入当前审核的流水附件、错误行/来源或改变的元数据停止。预览只读上传状态，缺记录不创建文件或锁；全部所选行通过才提供完整业务行。
- `Value.Artifacts` 将上传请求身份固定进计划版本，即使供应商返回相同 code，不同上传 provenance 也得到不同 UUID；旧计划没有该字段时保持兼容。来源 scope 仍保持物理 Base/Table，不把上传身份当来源 scope 绕过成员预约。

`prepare-approval-files` 先核对整个选择的当前 AI、类型化来源和分组，再校验每一组实时必填值，全部通过才上传。待上传附件只在本地模板副本中延后必填校验；每行分别要求有实际原件或已确认 code，不能由一行待上传掩盖另一行缺必填附件，也不使用假 code。模板原文不变，没有可执行实例请求。

上传按稳定选择顺序逐文件保存。后一份失败保留前一份成功结果；`retry-approval-files` 只给明确拒绝追加尝试，uploading/unknown 继续阻塞整个选择。上传结束重新核对来源与 AI，并验证完整原生表单；观测到变化就不返回可用计划。此检查仍非远端事务冻结，审批创建前须继续重核。

两个命令只输出安全状态、问题及计划摘要，`creation_available=false`。file_preparation_complete 与 form_validated 是不同结果，诊断 JSON 的成功输出不等于业务通过。完整配置与命令见[审批配置](../approval-configuration.md#审批附件准备)。

## 验证

隔离测试覆盖真实原始模板、全部组先校验再上传、逐行必填附件、精确金额、错误用途/目标/MIME/字节/路径、鉴权前阻塞、HTTP 跳转和未知响应、12 个并发状态句柄只发一次、跨重启复用、部分成功保留、拒绝显式重试、未知不可降级、旧历史与引用不可修改、取消后保存、意图及结果保存失败、过期 AI 和上传期间编辑、原件匹配、计划 provenance 及诊断隐私。

提交前发现并补测试：已有空/损坏上传文件不能被当作“没有记录”；缺失状态与损坏状态严格区分，损坏时阻止再次 POST。此前同一控制器局部延后 required 的实现也已加逐行检查，测试另一行缺附件时禁止整个批次上传。

以下均通过：

```text
go test ./...
go test -tags no_anthropic ./...
go vet ./...
go vet -tags no_anthropic ./...
go build ./cmd/server（默认及 no_anthropic，输出在忽略的 tmp/）
go test -race ./cmd/server ./internal/app/approval ./internal/core/approval ./internal/feishu/approval ./internal/feishu/base ./internal/state
```

真实验证只使用应用身份读取，不调用 OCR/审核方/上传/实例/通知：

| 检查 | 结果 |
| --- | --- |
| 新 no_anthropic 构建，当前企业配置 preview-approval | 两份 review_stale、一份 no_review；approval_not_configured，creation_available=false |
| 独立来源三行只读实验 | 原样本金额/币种/日期/员工/项目可投影；两份演示缺流水和项目；附件仍报告 approval_upload_required |
| 当前无 approval 配置的 prepare/retry 命令 | 在构造凭证/状态/网络前拒绝，exit=1；未上传 |
| 上述操作的状态目录前后 | 各次 17 个文件哈希全部未变 |

完整来源值与安全证据保存在私有 `data/public-debug/approval-source-private-values.json`、`approval-source-validation.json`、`approval-preflight-validation.json`、`approval-files-readonly-validation.json`，均为 0600，不进 Git。临时程序和验证二进制在忽略的 tmp；选中企业配置、原监督二进制及公网服务未变。隔离上传成功不替代真实 native file code 验收。

## 下一阶段

补完整实例请求审计快照与可审阅请求，然后按实际目标模板、员工/发起人/审批人和业务字段形成测试建单请求。现有企业样本没有当前可用 AI 结果，演示行缺交易/项目，旧模板对桥接应用不可读；这些条件不能用历史 Code、假币种/项目/白名单或跨应用 open ID 补齐。真实配置未齐时继续推进可验证的共用实现。

后续继续原 UUID 实例恢复、人工事件/状态与 Base/Seal 独立交付、版本冲突、撤回重提、占用/分摊/结算与权限锁定；T4 停机变更补偿和真正冻结、T5 模型 PDF/多页及提供方组合评估也仍待完成。此 commit 不宣称完整业务闭环完成。
