# 飞书模板配置

`approval-template.example.json` 是项目编写的审批模板创建请求，按飞书官方协议组织；它不是供应商原文或现有企业模板的导出文件。表单、字段 ID、文案、审批流程和可编辑设置均可在此修改。

使用独立工具 `go run ./cmd/approval-template -app personal -file configs/feishu/approval-template.example.json` 校验；显式加 `-apply` 才创建。凭证由进程环境注入，不写进 JSON。企业租户改用 `-app enterprise`。

完整运行约定与线上验证状态见 [`internal/feishu/approval/README.md`](../../internal/feishu/approval/README.md)，官方原文见 [`external-api/feishu/`](../../external-api/feishu/README.md)。
