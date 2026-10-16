# 远端公网认知调查与 AI Agent 运维使用手册

更新日期：2026-10-16  
适用版本：`v0.3.3+`  
适用受众：AI 运维 Agent、值班工程师、自动化监控系统

---

## 一、背景与设计理念

在企业级生产环境或云原生容器（如 K8s、企业级私有云 KVS/ECS）部署中，运维人员与 AI Agent **通常无法且严禁直接通过 SSH 登录远端服务器**。

为了让 AI Agent 能够以极低 Token 消耗、极快响应速度完成远程排障与认知调查，本项目内置了**专为 AI Agent 设计的轻量级认知调查接口套件**：
1. **机器友好认证**：全面支持标准 `Authorization: Bearer <token>` 请求头；
2. **低 Token 高密度输出**：支持 `format=markdown` 与 `format=text`，输出纯净的诊断文本，避免冗长复杂的 JSON 消耗上下文；
3. **最新日志优先（倒序检索）**：支持 `order=desc` 与关键词 `q=...` 过滤，秒级截取最新故障现场；
4. **单点全景透视**：提供 `/api/agent/diagnose`（全景健康快照）与 `/api/agent/inspect`（单据全生命周期下钻），无需串联多个零碎接口。

---

## 二、公网入口与安全认证

### 1. 服务基地址与配置
在 `configs/config.toml` 中配置公网地址与访问密钥：
```toml
[runtime]
public_base_url = "https://twc-approval.your-company.com" # 公司分配的公网二级域名
dashboard_password = "your-secure-password"                # 远端看板与 Agent 诊断密码
magic_token_ttl = "2h"                                     # 飞书私信动态 Token 有效时长
```

### 2. 鉴权方式（AI Agent 优先方式）
所有 `/api/*` 受保护接口均支持以下任一种鉴权凭据：

* **方式 A：标准 Bearer Token（推荐 Agent 使用）**
  ```http
  Authorization: Bearer <dashboard_password_或_magic_token>
  ```
* **方式 B：HTTP Header**
  ```http
  X-Dashboard-Token: <dashboard_password_或_magic_token>
  ```
* **方式 C：URL Query 参数**
  ```http
  https://twc-approval.your-company.com/api/agent/diagnose?token=<token>
  ```

> [!TIP]
> **如何为 Agent 申请临时免密 Token？**  
> 管理员可在飞书私聊中向自建应用的机器人发送 `"看板"`、`"status"` 或 `"logs"`，机器人核验身份后会动态生成一个带短时凭据的链接（形如 `?token=mt_xxxxxx`，默认 2 小时有效），可直接将该 Token 授予 Agent 执行特定调查任务。

---

## 三、AI Agent 核心调查接口清单

### 1. 存活性与就绪探针（无需鉴权）

| 路径 | 方法 | 作用 | 异常表现 |
| :--- | :--- | :--- | :--- |
| `/healthz` | GET | 进程存活检查 | 进程挂死时 HTTP 5xx 或无响应 |
| `/readyz` | GET | 依赖就绪检查 | **飞书长连接断开时返回 HTTP 503**：`{"status":"not_ready","reason":"..."}` |
| `/version` | GET | 当前发布版本与 Git 提交元数据 | 返回 `git_commit`、`build_date`、`go_version` |

---

### 2. 一键全景体检：`GET /api/agent/diagnose`

* **接口用途**：Agent 巡检或首轮排障时使用。**只需调用一次**，即可获得全盘运行体检、错误雷达及近期审计事件。
* **推荐参数**：
  * `format=markdown`（强烈推荐 Agent 使用，以极低 Token 获取高信息密度 Markdown 报告）
  * `format=json`（默认，结构化数据）

#### Agent 命令行调用示例：
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://twc-approval.your-company.com/api/agent/diagnose?format=markdown"
```

#### Markdown 输出样例：
```markdown
# TWC Approval Bridge - AI Agent 诊断快照
**总体健康状态**: HEALTHY
**快照时间**: 2026-10-16 10:15:00 (UTC+8)

### 1. 运行时指标
- **服务版本**: 0.3.3 (Commit: a1b2c3d, Built: 2026-10-16)
- **运行时长**: 2d 5h 32m
- **飞书长连接**: ready (已接收 1,420 个事件)
- **Anyreceipt OCR**: 剩余 2,450 点 (约可调用 245 次)
- **提供方编排**: 识别=anyreceipt | 审核=seal (after_recognition)

### 2. 异常雷达 (最近 WARN/ERROR 日志)
- 2026-10-16 10:05:12.102 [WARN] 员工白名单姓名与多维表格持卡人姓名不一致（以 open_id 为准放行） open_id=ou_123 whitelist_name=张三 actual_name=李四

### 3. 最近业务审计事件
- 2026-10-16 10:14:02 (UTC+8) [notify_card_sent] 向持卡人 谢子豪 推送交易补票卡片
- 2026-10-16 10:12:44 (UTC+8) [review_writeback] 审核结论成功写回飞书明细表
```

---

### 3. 单据全流程下钻：`GET /api/agent/inspect`

* **接口用途**：当收到特定单据疑问（例如“为什么这条报销没有出审核结果？”）时，针对指定 `record_id` 深度穿透。
* **请求参数**：
  * `record_id` 或 `id`（必填）：报销明细记录 ID 或流水记录 ID（如 `rec_xxxx`）
  * `format=markdown`（可选）：直接输出排障推断报告
* **聚合内容**：
  1. 审计流转全阶段时间线；
  2. 内存日志中与该单据相关的全部日志行；
  3. 自动提取最新动作与异常报错提示。

#### Agent 命令行调用示例：
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://twc-approval.your-company.com/api/agent/inspect?record_id=reczz28HBeFn4GdB&format=markdown"
```

#### Markdown 输出样例：
```markdown
# 单据调查快照: reczz28HBeFn4GdB

- **最新流转**: [review_writeback] 审核结论成功写回飞书明细表 (2026-10-16 10:12:44 (UTC+8))

## 审计时间线 (4 步)
1. 2026-10-16 10:11:02 (UTC+8) [file_uploaded] 员工上传了发票附件
2. 2026-10-16 10:11:05 (UTC+8) [ocr_completed] 发票台账写入成功
3. 2026-10-16 10:11:08 (UTC+8) [review_submitted] 提交 Seal 机审
4. 2026-10-16 10:12:44 (UTC+8) [review_writeback] 审核结论成功写回飞书明细表

## 关联日志行 (4 条)
- 2026-10-16 10:11:02.100 [INFO] received bitable record changed event record_id=reczz28HBeFn4GdB
- 2026-10-16 10:11:05.320 [INFO] anyreceipt recognition succeeded record_id=reczz28HBeFn4GdB amount=USD 15.00
- 2026-10-16 10:11:08.550 [INFO] seal review submitted document_id=doc_888 record_id=reczz28HBeFn4GdB
- 2026-10-16 10:12:44.210 [INFO] seal callback received and written back decision=PASS record_id=reczz28HBeFn4GdB
```

---

### 4. 高密度系统日志精准过滤：`GET /api/system-logs`

* **接口用途**：针对性定位系统错误日志，支持正/倒序排布、关键字过滤与单行格式化。
* **请求参数**：
  * `q` 或 `keyword`：关键字过滤（大小写不敏感，检索日志消息与所有 Attrs 属性，如 `q=error` 或 `q=rec_xxx`）；
  * `level` 或 `min_level`：最低日志级别过滤（`DEBUG` / `INFO` / `WARN` / `ERROR`）；
  * `order`：排序方向，填 **`desc`** 保证最新日志排在最前（Agent 排障首选）；
  * `limit`：拉取行数（默认 200，建议排查报错时设为 `20` 或 `50`）；
  * `format`：填 **`text`** 输出紧凑单行格式，省去 JSON 包装。

#### 常用实战查询：
```bash
# 1. 抓取最新的 20 条 ERROR/WARN 日志（纯文本、最新在前）
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://twc-approval.your-company.com/api/system-logs?limit=20&level=WARN&order=desc&format=text"

# 2. 精准检索某个流水号或单据的最近日志
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://twc-approval.your-company.com/api/system-logs?q=rec_xxxx&order=desc&format=text"
```

---

## 四、AI Agent 典型排障场景与推荐 Prompt 动作

| 场景 | 用户输入举例 | AI Agent 推荐执行动作 |
| :--- | :--- | :--- |
| **例行巡检** | “帮我看看远端服务现在状态正不正常” | 1. 调 `GET /api/agent/diagnose?format=markdown`<br>2. 查看 `总体健康状态`、长连接与 Anyreceipt 余额，直接提炼汇报 |
| **单据卡点** | “这张单据 `rec_abc` 怎么还没出 AI 审批结果？” | 1. 调 `GET /api/agent/inspect?record_id=rec_abc&format=markdown`<br>2. 若停留在 `ocr_completed`，检查关联日志判断是否因白名单拦截或 Seal 超时未回调 |
| **突发故障排查** | “系统刚刚好像报警了，查查最近的错误” | 1. 调 `GET /api/system-logs?limit=20&level=ERROR&order=desc&format=text`<br>2. 抓取最新真实报错堆栈进行根因诊断 |
| **员工催报核验** | “为什么张三没收到补票卡片？” | 1. 调 `GET /api/system-logs?q=张三&order=desc&format=text`<br>2. 查看是否命中 `skipped_not_in_whitelist` 或 `skipped_by_filter`（如 0 元流水或缺少必填 5 要素） |
