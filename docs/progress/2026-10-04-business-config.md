# 2026-10-04：业务来源配置文件

## 业务决定

用户要求流水、员工填写的表和发票台账都显式配置，便于后续更换文档、企业和提供方。保留已确认的识别完成自动送审，以及外部 Anyreceipt/Seal 主线与独立自有能力替换。

## 核验发现

通过项目应用身份只读查询当前企业 Base：共 6 张表，其中交易流水 `tblr0rFnvNntoSYR`、报销明细 `tbleThinekEbpK9L`、发票台账 `tblsCc7P4NRwJytk` 均位于 `K5EhbDdEKa8wbJsTCpmcmeVwnHc`。其余项目主数据、月度批次和基础档案不是当前运行链路的已实现来源。

原配置将明细和台账的 Base 合用一个环境变量，没有完整的角色配置，复制文档时容易混入旧字段。当前适配器仍要求明细与台账同 Base，不能通过增加配置字段宣称跨 Base 流程已实现。

## 实施

- 新增完整业务 JSON，显式声明三种角色的来源、访问约定、Base/Table/字段，以及识别和审核的独立提供方、触发方式。
- `BUSINESS_CONFIG_FILE` 原子选择整套业务绑定；选中后忽略对应旧环境配置，凭证仍从环境获取。保留未选文件时的兼容模式。
- 启动校验版本、必需字段、物理角色冲突、字段重复和结果覆盖输入；跨 Base 台账明确拒绝。
- `check-business-config` 使用应用身份分页读取字段元数据，核验字段存在、类型和明细/流水/台账关联目标；不调用付费能力或修改线上记录。
- 业务配置路径及说明名称不加入状态范围计算，避免迁移配置形式造成重复识别。

实现提交：`cd45de0 feat: configure explicit business sources and profile switching`。

## 验证与运行切换

- `go test ./...`、`go vet ./...`、`go test -tags no_anthropic ./...`、默认构建和外部主线构建均通过。新增测试覆盖完整配置切换、旧环境变量隔离、凭证保留、自动送审前提、未知属性、跨 Base 拒绝、错误字段类型、复制后关联错指及字段分页。
- 用新命令核验线上全部映射：流水 14 项、明细含审核配置 20 项、台账 18 项；附件类型 17、专用 AI 文本类型 1、台账有效字段类型及四个业务关联方向均通过。明细的两项上下文复用了输入映射，因此共 52 项映射、50 个物理字段。
- `.env.public-debug` 改为选择完整 JSON，移出对应旧业务覆盖项，保留运行参数和 0600 权限。密钥继续只放私有 `.env`；迁移前覆盖文件留存于忽略目录。
- 本机服务重建为 `public-debug-20261004-business-config` 并重启，启动日志确认 `enterprise-test`、Anyreceipt/Seal 与 `after_recognition`。本地和公网健康、就绪、版本接口均返回 200；WebSocket 重新连接。
- 新旧有效业务绑定及识别范围哈希完全一致，8 个持久化业务 JSON 文件内容完全一致；台账仍为 7 条，Anyreceipt 余额仍为 206，配置形式迁移没有创建识别或审核任务。

私有核验证据在 `data/public-debug/business-profile-check.json`、`business-profile-migration-before.json` / `after.json` 和 `business-profile-external-before.json` / `after.json`。这些文件及业务状态被 Git 忽略，文档只记录必要结论。

本次没有重新调用 OCR 或 Seal 送审；已有全自动链路的真实验收见[自动送审记录](2026-10-04-automatic-review.md)。当前新增配置和核验能力不代表交易事实、人工审批、结算、多配置并发或跨 Base 台账已经实现。
