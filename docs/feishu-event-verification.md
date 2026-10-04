# 企业测试 Base 的飞书事件验收

日期：2026-10-04，Asia/Shanghai。服务使用项目应用身份；没有进行用户 OAuth。

## 当前环境

| 配置 | 实际值 |
| --- | --- |
| 企业 | 测试企业zyt |
| 应用 | 影视飓风，`cli_aa4b551c9db85be4` |
| 已发布版本 | `1.0.1` |
| Base | `K5EhbDdEKa8wbJsTCpmcmeVwnHc` |
| 交易流水表 | `tblr0rFnvNntoSYR`；业务链路只读 |
| 个人报销明细 | `tbleThinekEbpK9L` |
| 发票附件字段 | `上传发票`，`fld9awqaDl`，附件类型 17 |
| 明细标识字段 | `明细ID`，`fldFR7zXuN`，自动编号类型 1005 |
| 发票台账 | `tblsCc7P4NRwJytk` |

先通过 `tenant_access_token` 核对全部表、字段及少量记录。用户提供的浏览器链接默认打开交易流水表，不能把该 Table 当作附件识别来源。

## 已修正的配置

1. 本机原来仍使用旧应用凭证，已更新私有 `.env`；原文件备份保存在被 Git 忽略的 `data/public-debug/feishu-before-enterprise.env`。
2. 新应用的事件列表为空，已添加应用身份的 `drive.file.bitable_record_changed_v1`，发布 `1.0.1`，核对长连接方式及发布状态。
3. 新 Base 的云文档订阅查询原为 `is_subscribe=false`；调用 `POST /drive/v1/files/<base>/subscribe?file_type=bitable` 后重新查询为 `true`。
4. 新应用只有细分 Base API 权限，缺少事件文档要求的两个身份的 `bitable:app`。已同时开通应用身份和用户身份该权限，并核对均为“已开通”。运行时继续只用应用身份。
5. 按新副本核对本机及示例的表和字段映射。台账补充文本列「识别来源键」（`fldDYxci6g`）和「识别原始JSON」（`fldFnFXBX2`），用于已有持久化识别流程。新副本的金额字段 ID 与旧环境含义不同：`fldjKawaXS` 是不含税金额，`fld1ZDUvQM` 是含税总额；没有修改金额值或重命名原列。

[飞书官方事件文档](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/drive-v1/file/events/bitable_record_changed)明确要求：以应用身份订阅时，后台也需要开通两个身份的 `bitable:app` 或 `drive:drive` 权限。该权限要求不表示必须取得用户访问令牌。

## 实际投递证据

在测试明细 `reczz28HBeFn4GdB` 上，通过应用 API 临时修改文本列「演示场景」，再读取当前值、确认仍为本次标记后恢复原空值。没有改动票据、金额、人工审核或交易流水。

补齐双身份权限前，修改并恢复该字段未看到事件；补齐后，本机长连接收到：

| 时间（+08:00） | 事件 ID | 类型 | 载荷大小 |
| --- | --- | --- | --- |
| 14:09:57.010256 | `6984c014ae026342d190a88a22c22e5b` | `drive.file.bitable_record_changed_v1` | 5446 字节 |
| 14:10:02.168603 | `356a6d1c4b4907f5458fcf6b214196ac` | `drive.file.bitable_record_changed_v1` | 5446 字节 |

日志保存在 `data/public-debug/com.yingqing.twc-approval-debug.log`，原始事件日志保持关闭。公网健康检查仍返回 `200 {"status":"ok"}`。

## 验收边界

已验证真实“记录修改 → 飞书事件 → 本机 WebSocket 监听器”链路，不能再将长连接未建立作为本次失败原因。旧个人版环境没有按同一组完整条件重测，因此不能认定个人版本身不支持该事件。

当前公网调试进程仍关闭 OCR 提供方，AI 结果列映射为空。修改普通文本列只验收事件，不会触发附件识别；新企业副本的附件新增、识别回写及完整财务流程仍需单独验收。旧副本中的历史台账不自动补造来源键或原始 OCR。
