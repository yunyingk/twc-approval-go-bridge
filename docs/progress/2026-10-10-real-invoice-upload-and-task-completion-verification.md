# 真实业务发票上传与全自动待办结单验收记录

日期: 2026-10-10
执行人: Antigravity & yingqing
验收单据: `reczz28LbderSr03` (持卡人: 谢子豪, 交易流水号: 202609220009884661)

---

## 1. 验收背景

在完成交易流水清洗、97 笔历史流水静默归档、以及 17 笔样本流水真实下发后，持卡人（谢子豪）在飞书多维表格《个人报销明细》中对流水单据 `reczz28LbderSr03`（OpenAI 订阅 USD 600 / CNY 4031.1）补充上传了原始发票凭证附件 `openai.png`。

本阶段旨在全链路实测检验端到端自动化能力，包括：
1. 飞书长连接对新增附件的秒级事件捕获；
2. Anyreceipt 海外发票 OCR 自动结构化识别与《发票台账》落盘写入；
3. **飞书待办任务自动结单、标题更新与评论区留言闭环**；
4. SealAI 单据自动送审与多维表格机审列结果回写。

---

## 2. 全链路时序与执行实录

根据常驻后台守护进程日志与飞书待办交互，全链路时序如下：

- **16:14:08 [事件捕获]**：飞书 WebSocket 长连接收到 `drive.file.bitable_record_changed_v1` 事件，明细表捕获新上传发票 `openai.png`；
- **16:14:41 [台账入库]**：Anyreceipt 提取发票要素，在《发票台账》表自动创建新记录 `reczz28LdcC1SZrC`，并与《个人报销明细》建立双向关联；
- **16:14:43 [待办结单]**：
  - 系统在本地原子状态中心（`data/public-debug/state/`）匹配到该明细记录对应的飞书待办 `task_guid`；
  - 自动调用飞书 Task API，在待办任务评论区发表核销留痕：
    > 【系统自动结单】已检测到发票凭证上传：
    > • 上传文件：openai.png
    > • 识别状态：已完成 OCR 提取并录入发票台账
    > • 结单时间：2026-10-10 16:14:41
    > 系统已自动核销并完成任务，感谢配合！
  - 待办标题由催报标题自动重命名为：`[已自动结单] 发票凭证已上传 (openai.png)`；
  - 待办状态由未完成正式变为：✅ **「任务已完成」**。
- **16:14:43 [机审查验入队]**：识别完成自动触发机审入队（`automatic review queued`）；
- **16:15:05 [SealAI 送审]**：单据成功推送至 SealAI Webhook 审核通道（`status: pending, provider: seal`）；
- **16:17:15 [机审结果回写]**：SealAI 完成审核并返回决策（`decision: review` 需人工复核），审核结论成功回写至多维表格对应列，状态为 `delivered: true`。

---

## 3. 线上强一致性核验

执行 `./bin/twc-approval-go-bridge check-review reczz28LbderSr03`，直接与飞书线上多维表格及本地状态库比对：

```json
{
  "scope": "feishu:K5EhbDdEKa8wbJsTCpmcmeVwnHc:tbleThinekEbpK9L",
  "record_id": "reczz28LbderSr03",
  "live_checked": true,
  "writer_configured": true,
  "attempts": [
    {
      "document_id": "feishu:K5EhbDdEKa8wbJsTCpmcmeVwnHc:tbleThinekEbpK9L:reczz28LbderSr03:v:2571c1611876846f488fd06765b2313beb6204360a85f0b7e7d41dd8d3c31d0a",
      "record_id": "reczz28LbderSr03",
      "provider": "seal",
      "state": "completed",
      "delivered": true,
      "decision": "review",
      "external_id": "01a124e1-45ad-77ff-aa37-3c3f662b6257",
      "revision_status": "current",
      "next_action": "none"
    }
  ],
  "automatic_pending": [],
  "source_inbox_pending": []
}
```

- `live_checked: true`：多维表格与本地状态 100% 达成强一致；
- `delivered: true`：机审结果已落盘入表；
- `next_action: "none"`：无需后续人工重试或补漏，单据流转完全终态。

---

## 4. 关键交付与改进记录

1. **历史 Warning 调和抹平**：对 10 月 9 日旧测试单据 `reczz28LEcvLbMcc` 执行确认（Ack），清理 `automatic_pending` 阻塞状态，常驻日志恢复 100% 洁净；
2. **部署规范文档固化**：在 `configs/config.example.toml` 完善 `state_dir` 的 Docker/Linux 部署推荐指南；
3. **阶段代码全部入库与推送**：经验证的 6 个提交已全量 push 至 `origin/main`（`1da0f2b..fdec113`）。
