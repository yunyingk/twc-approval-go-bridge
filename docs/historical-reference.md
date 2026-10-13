# 历史研究结论与适用边界

整理日期：2026-10-13。本文提炼外层资料目录中的早期实验和接口排查记录，保留有复用价值的结论，不替代当前接口文档、配置或验收记录，也不授权继续开发旧方案中的后续业务。

## 已由当前服务承接的能力

- 早期 `feishu-anyreceipt-ocr-shortcut` 是飞书字段捷径 FaaS 示例，从当前行第一个附件的临时 URL 调用 Anyreceipt，返回票据摘要及可选额度。当前识别编排由 `internal/app/recognition` 承担，OCR 客户端在 `internal/anyreceipt`，台账映射在 `internal/feishu/base/invoiceledger`，不依赖字段捷径运行环境。
- 早期 `feishu-approval-api` 只有一个 62 行表单转换脚本和研究 README，用于将旧实例展示结构转成普通 `fieldList` 控件值。当前表单由 `internal/feishu/approval` 根据业务事实和当前模板构造；该旧脚本不是运行依赖，也不是需要维护的第二套服务。
- Anyreceipt 接口摘要已由仓库内[供应商原版 OpenAPI](../external-api/anyreceipt/anyreceipt-openapi-v1.0.7.yaml)与[客户端说明](../internal/anyreceipt/README.md)承接。完整识别输出及原始响应继续保留，不因外层文档清理而改变识别或回写行为。
- 当前代码提供 `preview-approval` 和 `push-approval`，范围是[单向审批推送](../cmd/server/approval_cmd.go)。早期草案中的审批状态回调、资金结算闭环和批次 Excel 附件路线没有纳入本次清理，也没有因文档删除而自动实现或启用。

## 旧审批结构的失败证据

来源：2026-08-03 的《飞书审批-引用多维表格-mutableGroup-API限制》及早期审批脚本研究记录。

- 历史实例的 `mutableGroup` 明细曾能通过查询接口读取；这不证明相同展示结构可以用于创建请求。
- 历史实测中，删除该控件后创建请求返回 `1395006 validate form error`；直接照搬详情结构的另一种请求虽然返回成功，新实例的 `form` 却为空。HTTP 或业务成功响应不能单独作为表单内容正确的验收证据。
- 当时已决定停止逆向客户端私有提交接口，不再处理原审批与历史测试实例。这个决定仍作为研究边界保留。
- 编写新的请求应核对目标审批定义和公开控件协议，传实际控件值，不能沿用旧模板的 ID、展示元数据或附件临时下载 URL。仓库内[创建实例原文](../external-api/feishu/create-approval-instance.md)和[控件协议原文](../external-api/feishu/approval-instance-form-controls.md)用于追溯当时的公开协议。

以上只记录当时证据；本轮未查询线上模板或重新验证飞书目前是否支持某个控件。

## 身份与权限排查的有效结论

来源：《飞书审批API权限与用户部门约束排查指南》。保留错误类别与排查方向，不保留旧人员、旧应用和部门配置为当前默认值。

- `1390001 user not found`：旧联调中与人员控件的部门选择范围有关。排查时需要同时核对人员标识所属应用、实际人员与控件可选范围，不能仅凭错误文案认定用户不存在。
- `41050 no user authority error`：旧联调中与应用的通讯录数据范围有关。接口权限和资源数据范围需要分别核验；历史“全部员工”设置不是当前项目的默认授权要求。
- `99991668 user access token not support`：旧原生审批请求曾因 token 类型被拒绝。当前服务仍按项目规则使用应用身份 `tenant_access_token`，不采用旧指南中的用户 OAuth 或 `--as user` 路径。

这些排查方向不授权更改线上通讯录、审批范围、应用权限或用户身份。

## 业务草案的保留边界

外层仅保留 Seal 风控和 TO-BE 泳道两份历史业务草案，其原文有助于回溯需求。草案中的汇率容差、金额阈值、自动审批比例、人员归属与财务流程未在本轮核验，不得作为当前已生效的规则。Seal 规则继续由外部维护；正式业务范围以用户后续明确确认和实际验收为准。

外层旧文件原文、两个实验仓库的 Git 历史与未提交改动有本地私有备份。具体清理清单、验证结果和恢复位置见[本轮记录](progress/2026-10-13-workspace-cleanup.md)。
