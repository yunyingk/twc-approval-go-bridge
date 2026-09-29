# 可选的 Anthropic 兼容能力

`client.go` 封装 Anthropic Messages SDK；`model/` 将图片识别结果转换为 `core/invoice.Recognition`。只有选择模型提供方时才在运行时使用，`no_anthropic` 编译标记会排除整个能力及 SDK。
