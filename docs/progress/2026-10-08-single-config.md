# 配置单文件与旧入口收敛

2026-10-08，Asia/Shanghai。用户指出项目尚未上线，不应承担旧入口兼容债务，并要求所有项目设置使用一个配置文件。本次只收敛配置和已迁移入口，不扩展人工审批或财务业务。

## 兼容包装的来源

- `24d5489`（2026-10-04）把识别调度从 Anyreceipt 移至 `app/recognition`，台账映射移至 `feishu/base/invoiceledger`，审核编排从 Seal 移至 `app/review`；目的是支持外部能力与自有模块独立切换。提交仍保留旧包类型别名、包装和不版本化的联调命令，旧调用主要来自测试。
- `cd45de0`（同日）加入完整业务 JSON，但继续支持未选文件时的环境变量业务配置；凭证和运行参数还由多个 env 文件叠加。
- 两次变更均是项目内部迁移，没有已上线客户或外部调用方要求保持旧入口。应直接完成迁移，历史实现由 Git 留存。

## 当前唯一配置

运行只读取私有 `config.json`，权限 0600、Git 与 Docker 构建忽略；仓库仅保留完整脱敏 `configs/config.example.json`。CONFIG_FILE 只选择该文件的路径，不覆盖文件内的值。缺失、无效或未知字段直接报错，不回退到旧配置。

同一文件包含 runtime、feishu、tables、recognition、anyreceipt、seal、review 和可选 approval。模型连接分别内嵌于 recognition.model/review.model，自有审核规则在 review.rules。Seal 主线不读取这些规则；SealAI 继续维护自己的规则。模板请求内嵌 feishu.approval_template；独立模板工具改为显式选择 bridge/approval 凭证，不再读取另一份请求文件，仍需 -apply 才创建。

本机从原示例、.env、.env.public-debug 与企业业务文件的有效配置迁入 config.json，保留所有实际凭证、资源 ID、状态目录、模型端点、轮询与送审方式。原私有文件后续只作归档，不作为后备。识别仍为 Anyreceipt/both，审核仍为 Seal/after_recognition，include_transactions=true，两个修改重审开关关闭，人工审批未启用。

## 入口与测试迁移

- 移除 Anyreceipt 旧 flow/ledger 包装；事件与轮询回归测试迁入真正的共用识别层，台账重复测试移除，原台账文档移入实际实现目录。
- 移除 Seal Service/New/类型别名；测试直接装配 Gateway 与共用持久化用例。
- 移除 submit-seal。显式 submit-review 与自动送审统一使用版本化请求、状态保存与结果交付，装配函数不再提供 versioned=false 参数。
- 启动包装器不再读取任何 env 文件；Docker 挂载一份 JSON，发布包也只携带一份完整示例。旧配置注释与模板文档保存在[迁移前注释](2026-10-08-previous-config-notes.md)，历史阶段资料不再是有效启动说明。
- 保留真实供应商协议解码、既有识别/审核状态和测试租户联调证据；提供方切换继续存在，不删除业务数据。

## 验证

默认构建与 no_anthropic 构建的 go test ./...、go vet ./... 及服务器构建通过。配置测试覆盖文件独占凭证/参数、旧环境变量无效、缺文件不回退、自动送审约束、字段保护、内嵌规则及私有 JSON 错误不回显值。模板示例只读校验通过，没有创建请求。

新二进制使用 config.json，以项目应用身份执行 check-business-config 成功核验流水 14、明细 20、台账 18 个字段；review-status 读到原两份 completed/delivered 的 Seal 结果，无自动意图或来源收件待处理。状态只读诊断未重新确认历史版本的当前事实，没有新增付费识别/审核或线上业务修改。

监督服务切换与重启后的公网、本地、WebSocket 验证另在本记录追加。
