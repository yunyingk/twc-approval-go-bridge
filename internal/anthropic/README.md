# 可选的 Anthropic 兼容能力

`client.go` 封装 Anthropic Messages SDK；`model/` 将图片识别结果转换为 `core/invoice.Recognition`。只有选择模型提供方时才在运行时使用，`no_anthropic` 编译标记会排除整个能力及 SDK。

`review/` 实现自有审核，使用同一 config.json 内独立 `review.model` 配置和内嵌 `review.rules`；它与识别模块共用客户端，业务编排在 `internal/app`。Seal 主线不读取本地规则。JSON 解析兼容单个 Markdown 围栏，并拒绝截断响应。当前运行边界见 [运行与交接说明](../../docs/runtime-and-handoff.md)。
