# 可选的 Anthropic 兼容能力

`client.go` 封装 Anthropic Messages SDK；`model/` 将图片识别结果转换为 `core/invoice.Recognition`。只有选择模型提供方时才在运行时使用，`no_anthropic` 编译标记会排除整个能力及 SDK。

`review/` 实现自有审核，与识别模块共用 `configs/config.toml` 的全局 `[model]` 连接配置；审核规则由 `[review] rules_file` 指向独立 JSON。相对路径优先按工作目录查找，未命中时相对主配置目录解析，没有独立 `review.model` 连接段。业务编排在 `internal/app`。Seal 主线不读取本地规则。JSON 解析兼容单个 Markdown 围栏，并拒绝截断响应。当前运行边界见 [运行与交接说明](../../docs/runtime-and-handoff.md)。
