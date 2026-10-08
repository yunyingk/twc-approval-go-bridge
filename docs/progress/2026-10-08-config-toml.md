# TOML 配置收尾与 Gemini 交接

2026-10-08，Asia/Shanghai。用户要求模板与自有审核规则独立，主配置选用支持注释和分节的 TOML，提供方选择放在最前面；随后要求最小修改、尽快结束并移交 Gemini review。本阶段限定于这些配置改动和必要读取适配。

## 文件与读取

- 私有 `config.toml`：0600、Git/Docker 忽略。顺序为 recognition/review 提供方与触发方式、连接凭证、runtime、可选模型连接、表及字段映射。脱敏示例为 `configs/config.example.toml`。
- `templates/feishu/approval-template.example.json`：原模板请求独立保存；一次性工具显式 `-file` 选择，默认校验不读主配置，只有 `-apply` 才读取凭证并创建。运行服务不加载模板。
- `rules/review.example.json`：原自有示例规则独立保存。仅 `review.provider = "model"` 时构造审核方并读取 `review.rules_file`；相对主 TOML 所在目录解析。SealAI 规则仍在外部系统维护。
- 主加载器只解析 TOML，无 JSON 或环境变量回退；`CONFIG_FILE` 只选择文件。采用 go-toml/v2 v2.4.3 的严格结构解码；重复键/节、未知结构字段与错误类型报错，不回显私有解析内容。[解析器官方文档](https://pkg.go.dev/github.com/pelletier/go-toml/v2)。

私有 TOML 与拆出模板/规则后的私有 JSON 经 Python tomllib 逐项比对相同，实际凭证、Base/Table/Field、模型参数、状态目录及识别/送审策略未变。当前仍为 Anyreceipt/both、Seal/after_recognition、流水参与审核、两个修改重审开关关闭、人工审批未启用。旧私有 JSON 和已有私有备份保留，不提交。

## 验证

- 默认与 `no_anthropic` 的 `go test ./...`、`go vet ./...`、服务器构建全部通过。
- 新二进制读取私有 TOML 执行本地 `review-status` 成功，8 个状态 JSON 内容不变。
- 模板独立校验通过，使用不存在的 CONFIG_FILE 仍可校验；没有创建模板或调用付费提供方。
- Git diff 检查与凭证扫描通过；私有文件不进入提交。

## 部署与后续 review 边界

按用户最后要求，本阶段到配置提交即结束，没有重启或替换监督服务。`deploy/run-local-debug.py` 和 `bin/twc-approval-public-debug` 仍是上一阶段的 JSON 版本，读取保留的 `config.json`；运行部署证据见[前阶段记录](2026-10-08-single-config.md)。新代码默认读取 `config.toml`，Docker 挂载和发布示例已同步。后续部署需同时更新监督二进制与包装器的文件选择，不能只替换其中一项。没有在新加载器内保留 JSON 兼容入口。

供 Gemini review 的已知现状：表字段映射仍不是完整语义白名单；禁用能力的部分参数仍沿用原校验方式。`cmd/server/main.go` 的识别范围计算还包含模型名称/端点，即使选择 Anyreceipt；本次保留原值以免改变基线范围。这些问题没有在格式调整中扩大修改。人工审批/财务后续继续遵守原收尾边界。
