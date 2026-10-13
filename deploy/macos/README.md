# 本机后台应用与飓风图标

`TWC Approval Bridge.app` 由 `scripts/local-service.py` 按当前仓库构建，产物位于被 Git 忽略的 `bin/local-service/releases/`。应用包只保存程序、图标及项目路径，不复制私有配置或业务状态。

`launcher.m` 读取生成的 `TWCProjectRoot`，恢复原工作目录和默认 `CONFIG_FILE`，然后在同一 PID 内 `exec` 原 Go 二进制。应用通过 Launch Services 以 `LSBackgroundOnly` 启动，macOS 保留应用身份；`app-info.m` 用于检查实际应用 PID、名称、包路径和图标。业务 Go 代码及 Linux/Docker 构建不需要引入 AppKit 或 CGO。

Go 二进制及应用包分别进行本机 ad-hoc 签名并执行严格验证后才允许切换。这是本机构建签名，不是 Apple Developer ID 签名或公证；不修改 macOS 安全设置。

## 图标来源

- `icon.png`：用户指定通过 ChatGPT 内置 imagegen 生图生成的原创极简扁平飓风图标，外部透明区域保留；没有使用影视飓风公司的现有 Logo。
- `AppIcon.icns`：通过 macOS `sips` 和 `iconutil` 从原图生成 16、32、128、256、512 点及各自 2x 尺寸，作为应用图标资源。
- 原图与最终 `.icns` 均在本仓库内保存，构建不依赖 Codex 图片目录。

最终生成提示词（内置工具模式）：

> Use case: logo-brand. Asset type: macOS background service application icon for TWC Approval Bridge. Create one original extremely minimal flat hurricane symbol, as requested by the user. Square 1024x1024 canvas. A single bold white stylized hurricane spiral with a large round central eye and two sweeping tapered arms, centered on a solid deep cobalt blue rounded-square tile. Balanced iconic silhouette, broad simple shapes, clean sharp edges, strong legibility at 16x16 and 32x32 pixels. Strictly flat solid colors, no gradients, no 3D, no shadows, no fine lines, no text, no letters, no watermark, no existing company logo. The tile fills about 88% of the canvas with transparent space outside its rounded corners. Produce only the finished app icon, not a mockup, not a grid, not multiple variants.

工具参数为 `transparent_background=true`。实际原图为 1254 × 1254 RGBA；打包时从这份原图等比例生成系统图标尺寸，不修改图形设计。
