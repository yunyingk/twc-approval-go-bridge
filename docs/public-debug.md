# 本机公网调试入口

配置日期：2026-10-03。此入口把 HTTPS 请求接到当前 Mac 上运行的桥接服务，使用现有阿里云公网服务器，不经 Windows WSL。

```text
https://twc-approval.ying-qing.cn
  → 阿里云 118.31.4.210 nginx（HTTPS）
  → frps:8080
  → 本机独立 frpc：twc-approval-mac15-web（TLS）
  → 127.0.0.1:18088 桥接服务
```

DNS 已解析到 `118.31.4.210`。nginx 当前采用域名白名单，因此新增独立虚拟主机；没有修改已有服务的域名列表或重启共享 FRP。部署的 nginx 配置见 [public-debug.conf](../deploy/nginx/public-debug.conf)。

## 可访问接口

- [健康检查](https://twc-approval.ying-qing.cn/healthz)：`200 {"status":"ok"}`。
- [就绪检查](https://twc-approval.ying-qing.cn/readyz)：`200 {"status":"ready"}`。
- [构建版本](https://twc-approval.ying-qing.cn/version)：当前运行 `public-debug-20261004-review`。
- `POST /seal/callback/<密钥>`：密钥在本地私有配置及 Seal `test` 通道保存，不写入本文。

公网根路径、mock 与其他路径返回 404；错误回调密钥返回 401；正确密钥搭配无效内容返回 400。nginx 限制请求体为 1 MiB，禁止记录该域名的访问 URL 和错误 URL；应用已有回调路径遮蔽。不要把完整回调 URL 复制到日志或公开文档。

## 进程与私有配置

两个独立 LaunchAgent 在用户登录后启动，并在进程退出后恢复：

| 进程 | Label | 配置/入口 |
| --- | --- | --- |
| 桥接服务 | `com.yingqing.twc-approval-debug` | `deploy/run-local-debug.py` |
| 本机 FRP | `com.yingqing.twc-approval-frpc` | `data/public-debug/frpc.toml` |

plist 位于 `~/Library/LaunchAgents/`。运行日志在 `data/public-debug/`，识别与审核状态在 `data/public-debug/state/`。FRP 配置、`.env` 及 `.env.public-debug` 权限为 0600，均被 Git 忽略。项目凭证统一保存在 `.env`；`.env.public-debug` 只保存调试参数，不重复或清空凭证。FRP 复用已有服务器鉴权，未修改密钥。

[运行包装器](../deploy/run-local-debug.py)明确依次加载 `configs/config.example.env`、`.env`、`.env.public-debug`，后者覆盖前者；不执行 shell 展开。Go 程序本身仍不自动读取 `.env`。

本调试进程启用 Seal 结果接收及 `RECEIPT_PROVIDER=anyreceipt`，`RECEIPT_TRIGGER_MODE=both`；首次扫描使用 `baseline`，已有附件不会因此批量识别。`REVIEW_RESULT_FIELD_IDS` 已映射七个专用文本列，真实审核结果保存到本地状态并回写 AI 建议，不覆盖人工审批或结算字段。字段映射与验收见[阶段过程记录](progress/2026-10-04-seal-review.md)。当前二进制使用 `no_anthropic` 构建，调试外部系统主线。

```bash
# 查询进程
launchctl list com.yingqing.twc-approval-debug
launchctl list com.yingqing.twc-approval-frpc

# 修改 Go 代码后重新构建，再仅重启自己的服务
go build -tags no_anthropic -ldflags '-X github.com/yunyingk/twc-approval-go-bridge/internal/version.Version=public-debug-20261004-review' -o bin/twc-approval-public-debug ./cmd/server
launchctl kickstart -k gui/$(id -u)/com.yingqing.twc-approval-debug

# 使用同一套私有配置与状态目录手动送审
python3 deploy/run-local-debug.py submit-review <报销明细记录ID>
```

服务可用依赖本机开机、联网和用户登录。电脑睡眠或离线时，该调试域名不能到达本机服务；这是本机调试入口，不是全天候生产部署。

## 企业测试应用切换（2026-10-04）

本机服务已改用企业测试应用及新 Base，私有配置中的表和字段 ID 已重新核对。真实记录变更事件已收到，详见[飞书事件验收](feishu-event-verification.md)。原有公网地址、FRP、Seal 通道及状态目录继续使用；OCR 已完成[新副本验收](anyreceipt-enterprise-verification.md)，七个 AI 专用结果列已启用并完成真实回写。原应用配置的私有备份保存在 `data/public-debug/feishu-before-enterprise.env`，不提交 Git。

## Anyreceipt 凭证恢复（2026-10-04）

Anyreceipt 的密钥已于 2026-10-04 从 2026-09-29 的历史联调会话恢复，现与飞书、Seal 凭证统一保存在私有 `.env`（0600）。恢复时通过 `GET /api/getApiKeyUsage` 验证 HTTP 与业务状态均为 200；此后已启用识别并完成新企业副本的真实 OCR 和台账回写验收。密钥不保存到文档或 Git。

## Seal 通道与验收

已把 `wh_1790692906316_6z2eao6`（`test`）的回调从本机 mock 更新到公网密钥 URL，并刷新页面核对保存成功；其他测试通道未改动。审批模式仍为辅助模式，规则继续在 Seal 内维护。

使用已有测试明细 `reczz28HJVvAm8NM` 经 `submit-review` 生成版本化任务；提交成功，原件 1 个、结构化发票 0 张、历史候选 0 个。结构化发票缺项的限制沿用历史样本事实，不补造币种。

同日真实 Seal 回调已抵达，任务由 `pending` 变为 `completed`，结论为 `review`（人工复核），包含真实审批记录 ID 与 Seal 详情 URL。使用同一回调内容从公网重复投递，返回 `200 {"success":true}`，保存状态完全一致。

2026-10-03 此次没有配置飞书 AI 输出列，因此 `delivered=false` 表示没有进行表格交付，不是回调失败。后续企业副本已完成专用 AI 字段回写，任务为 `completed`、`delivered=true`；旧来源结果继续保留归档。完整财务流程仍需另行验收。
