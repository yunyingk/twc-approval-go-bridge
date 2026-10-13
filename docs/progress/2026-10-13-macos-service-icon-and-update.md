# 2026-10-13 Mac 后台服务图标、快捷更新与回滚

## 已确认范围与起点

用户确认完成本机图标、快捷更新／查询／回滚工具及首次运行切换，随后指定通过 ChatGPT 内置生图生成极简扁平飓风图标。Windows WSL Docker 迁移暂缓，本轮没有向远程主机部署或调整公网入口。

起点 HEAD 为 `fa29808`，工作区干净。运行进程为 `0.3.3 / 0cbd7e5`，磁盘二进制为 `0.3.3 / 973ad44`，二者不同。现有 LaunchAgent 为 `com.yingqing.twc-approval-debug`，通过 Python 包装器 exec Go 程序，监听 `127.0.0.1:18088`。私有配置、表拓扑和 140 个业务状态文件均保持现有位置。

## 实际修改与原因

- `deploy/macos/icon.png`、`AppIcon.icns`：内置 imagegen 生成原创极简飓风图标，保留原图透明区域，转换为 macOS 多尺寸图标。没有下载或改编现有公司 Logo；完整提示词和实际尺寸见[资源说明](../../deploy/macos/README.md)。
- `deploy/macos/launcher.m`、`app-info.m`：补充后台应用身份；启动器设置原工作目录及主配置路径后在同 PID 内 exec Go 二进制，检查工具读取实际应用 PID、名称、包路径和图标。不修改业务 Go 代码，不给 Linux/Docker 引入 AppKit 或 CGO。
- `scripts/local-service.py`：构建独立候选应用；签名并严格验证 Go 可执行文件与应用包；备份可恢复版本与 LaunchAgent；串行优雅切换、状态查询及回滚；核对健康、完整构建元数据、飞书连接、唯一进程和应用图标。所有备份、锁与运行记录均在 Git 忽略目录内，敏感配置不进入应用包。
- `scripts/test_local_service.py`：覆盖原子替换失败、外部路径和无关进程保护、检查失败不停止原服务、候选失败恢复、回滚目标交换、版本及图标验收等关键失败边界。
- `Makefile`、`deploy/run-local-debug.py`：新增 `local-update`／`local-status`／`local-rollback`；服务模式等待后台应用退出，非服务 CLI 与无活动应用时的旧入口保持可用。
- `docs/runtime-and-handoff.md`、`CHANGELOG.md`：记录当前操作方式、签名与回滚边界、程序备份和业务状态的区别。

## 已执行验证

- 12 项 Python 隔离测试通过；Python 源码 AST 解析通过。
- `go test ./...`、`go test -tags no_anthropic ./...`、`go vet ./...` 均通过；Go 测试主要使用缓存。原生启动器与检查器使用 `-Wall -Wextra -Werror` 编译通过。
- 真实 Go 候选应用在独立端口 `19090`、无凭证／无业务触发的测试配置下启动，HTTP 版本与候选匹配，飞书状态为 disabled；同 PID 应用身份和图标存在，发送 SIGTERM 后退出，等待进程同步结束。
- Go 程序及应用包的 `codesign --verify --deep --strict` 通过；从包内复制出的独立 CLI 同样验证及版本查询通过。
- 首次执行真实 `make local-update` 成功，完整检查、构建、备份、切换与飞书恢复合计 **8.01 秒**。运行版本为此次尚未提交源码的 `fa29808-dirty`，实际 Go PID `35258`；飞书状态 ready，健康接口正常。
- 通过 macOS 活动监视器原生 UI 搜索，实际看见 `TWC Approval Bridge` 名称及蓝色飓风图标，唯一 PID 与 HTTP 监听进程一致。
- 初次切换后，私有配置、表拓扑及 140 个既有状态文件共 **142 个文件 SHA-256 全部一致**。
- 真实执行 `make local-rollback` 成功，恢复此前保存的磁盘版本 `973ad44`，实际 PID `35905`，飞书状态 ready；耗时 **2.07 秒**。此回滚目标不是起点运行进程的旧内存镜像 `0cbd7e5`。

阶段提交后使用相同 `make local-update` 构建并切换与提交号一致的最终应用。最终可变 PID、构建提交号、验收时间和回滚版本由工具写入私有 `data/local-service/last-operation.json`；可通过 `make local-status` 独立复查，避免将后续变化的进程号写死在操作说明中。阶段提交不包含运行数据、配置、LaunchAgent、程序备份或构建产物。

## 实施问题、取舍与未覆盖部分

初次图标探测临时复制系统 `sleep` 程序放入应用包；原签名没有新增包资源的正确封印，macOS 显示“IconProbe.app 已损坏”，用户点击取消。已明确该弹窗来自临时包，不是业务服务损坏；通过分别签名执行文件与应用包并严格验证解决，正式工具在切换前执行这些检查。没有修改系统安全策略或要求用户绕过安全警告。

`open -W` 是 LaunchAgent 等待进程，真正 Go 服务由 Launch Services 管理；二者 PID 不同。卸载 LaunchAgent 后还需对已验证的本项目应用发送 SIGTERM，并确认旧实例完全退出，才能保证单实例。启动适配只在同一 PID 内短暂执行，不常驻另一个业务服务；保留原配置、审计／识别状态、日志、公网地址和后台任务恢复。

相对确认方案，增加必要的原生身份检查器与 LaunchAgent 退出等待时间，均属于本机启动层；业务、配置契约和跨平台构建范围未扩大。完整检查会使更新时间随测试、编译缓存和网络连接恢复情况变化，8.01 秒只是本轮实测；本机 ad-hoc 签名不等于 Developer ID 分发签名或 Apple 公证。

自动回滚的失败路径由隔离测试注入故障验证，真实常驻服务执行的是显式回滚；没有为了验收主动破坏正式进程或注入故障。未增加 OCR／Seal 付费测试，未发送测试通知、创建审批或待办。没有验证 Mac 注销／重新登录、电脑重启、休眠恢复或异地分发应用。正常业务恢复继续遵循原服务的持久化边界。
