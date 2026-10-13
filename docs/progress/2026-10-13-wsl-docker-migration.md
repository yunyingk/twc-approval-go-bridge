# 2026-10-13：Mac 本机服务迁移至 WSL Docker（Linux 演练）

## 背景与范围

按已确认方案，将常驻服务从 Mac LaunchAgent 迁移到家里 Windows 主机上的 Ubuntu WSL 实例，作为未来公司服务器部署的 Linux/Docker 演练。迁移成功后停用 Mac 的两个专用后台服务（业务 worker `com.yingqing.twc-approval-debug` 与公网转发 `com.yingqing.twc-approval-frpc`）。共享转发容器 `frpc-wsl-gateway` 与无关的 `com.yingqing.frpc` 不在范围内，未改动。

## 关键技术发现

1. **`TestFormatHelpers` 隐式依赖东八区**：`formatTime` 按进程时区格式化毫秒时间戳，测试期望值 `2026/09/28` 只在 UTC+8 成立（该时刻为 UTC `2026-09-27 16:00`）。Mac 构建通过、Linux 容器（UTC）构建差一天失败，首次迁移在 `prepare` 阶段被候选镜像的双构建守卫拦截——守卫按设计生效，失败发生在停止 Mac 服务**之前**，线上零影响。修复：测试内固定 `time.Local = UTC+8`（`f927015`），UTC 与东八区均通过。
2. **运维测试夹具覆盖缺口**：`test_compose_uses_release_tag_but_verifies_immutable_image_id` 调用了夹具里已被 Mock 替换的 `host.compose`，断言恒失败；改为直接调用未绑定的真实方法 `remote.Host.compose(host, ...)` 后 25 项测试全部通过。
3. **私有配置防护**：`.dockerignore` 排除 `configs/config.toml`、`configs/tables/*.json`、`configs/rules/*.json`（保留 `.example` 模板），私有 TOML 与业务状态只经 SSH 传输，不进 Git、不进镜像层。

## 实施与验收记录

- 提交 `8c7a817`（迁移与远端版本管理工具）、`f927015`（时区测试修复）；阶段守卫 `go test ./...`、`go test -tags no_anthropic ./...`、`go vet ./...`、25 项 Python 运维测试全部通过。
- `make remote-migrate` 流程：本机全量检查 → SSH 上传已提交源码与改写后的私有配置（`http_addr=:8080`、`state_dir=/data`、`tables_file`/`rules_file` 指向 `/configs/…`）→ WSL 构建镜像并核验版本元数据与平台 → 离线冒烟容器（业务触发关闭、飞书 disabled）→ `check-business-config` 应用身份只读核验 → FRP 配置校验 → 停 Mac 两个专用服务 → 140 个状态文件 SHA-256 逐文件比对 → 启动 Compose（server + 专用 frpc）→ 健康/就绪/飞书/公网核验 → 禁用 Mac LaunchAgent。
- 验收结果：远端 `git_commit=f927015c713a`、`platform=linux/amd64`、`feishu_state=ready`、`public_verified=true`；公网 `https://twc-approval.ying-qing.cn/version` 返回同一提交号；Mac 专用任务已从 `launchctl list` 消失、本机 18088 无监听；运行镜像为不可变 ID `sha256:1171692b…`，与 `operations/active.json` 记录一致。
- 迁移证据留存：`data/remote-migration/{migration.json,state-manifest.json,mac-state.tar.gz}`（0600，`.gitignore` 保护，不入库）。

## 边界与后续

- 本次为 Linux 演练：公司生产的网络、权限、域名与持久化条件仍需届时核验；`runtime.public_base_url`、业务事实与配置表不参与代码回滚。
- 后续维护命令：`make remote-status`／`remote-update`／`remote-rollback`；三者均已实操演练：`remote-update` 将远端从 `f927015` 切到 `009a24d`（新镜像构建、验收、原子切换），`remote-rollback` 恢复到 `f927015`，再次 `remote-update` 回到 HEAD；每次切换后 `/healthz`、`/readyz`、飞书长连接与公网 `/version` 均重新核验通过，业务状态目录在切换间保持不变。
