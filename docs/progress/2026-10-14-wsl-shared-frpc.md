# 2026-10-14：WSL 统一使用共享 frpc

用户确认将 TWC 专用 frpc 合并到现有全局网关。核对时共享网关为 host 网络，已有 8 条代理；TWC 原专用容器只承载一个域名。18088 端口无占用。

实施：保留配置备份，给全局网关追加一个 TWC 路由；业务绑定 localhost 18088；使用主机级 Compose 覆盖保证旧镜像回滚仍使用共享入口。运维管理器回滚时保留当前实现，避免重新启用专用 frpc。公网 frps 和业务私有配置、状态均保持原状。

验收（2026-10-14）：

- 代码提交 `a978d8d`；本地及 Linux 镜像均通过 Go 双构建全量测试、`go vet`，26 项 Python 运维测试通过。
- 全局网关追加 `twc-approval-wsl-web`，原有 8 条代理结构逐项保持一致；9 条代理配置经 `frpc verify` 通过。切换前后原有域名 HTTP 状态一致（6 个 200、MinIO API 403、静态站根路径 404），只核验入口响应，不声明各产品内部功能全量验收。
- 停止并移除 `twc-approval-bridge-frpc-1`；业务只绑定 `127.0.0.1:18088`。公网版本为 `a978d8d2b602`、飞书连接 ready、健康及就绪检查通过。
- 实际执行 `make remote-rollback` 回到 `926c5e1`，再 `make remote-update` 回到 `a978d8d`；两次均通过公网与飞书核验，端口保持 18088，专用 frpc 没有重建，共享网关不随应用更新或回滚重启。
- WSL 私有备份：`operations/shared-gateway-backup-20261014T015802Z/`；入口切换证据：`operations/shared-ingress-cutover.json`。备份和凭据不入 Git。

公网 frps 未修改；业务配置和状态没有回退；未触发合成 OCR、审核或真实 Seal 回调测试。当前仅核验公网路由、运行就绪和已有业务连接。
