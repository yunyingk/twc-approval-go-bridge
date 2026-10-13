# 飞书能力边界

- `base/`：多维表格（Base/Bitable）的记录、附件、台账和记录变更事件。
- `approval/`：当前审批模板的表单映射与实例创建客户端；由 `cmd/server` 的 `preview-approval` / `push-approval` 显式调用，不包含旧模板初始化工具或结果监听。
- `events/`：Base 及机器人事件共用长连接；`card/`、`task/`、`notify/`、`bot/` 分别承担补票卡片、待办、告警和机器人交互。

多维表格与原生审批属于不同产品接口。OCR／AI 审核主线读写 Base，原生审批为独立显式单向提单；SealAI 审核提交和结果接收位于 `internal/seal/`，不等于飞书人工审批或财务结算。当前配置与命令见[审批说明](../../docs/approval-configuration.md)。
