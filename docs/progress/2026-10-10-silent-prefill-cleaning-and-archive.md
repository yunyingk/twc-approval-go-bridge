# 方案 A 落地：个人报销明细批量静默建单清洗与样本保留

**日期**：2026-10-10  
**状态**：已执行完成，线上多维表格双向关联已生效，双构建与 go vet 验证通过  

---

## 1. 业务背景与原则

针对线上《交易流水表》中 114 条有效待处理流水（谢子豪 97 条、李俊 11 条、宋国杰 6 条）：
1. **原则红线**：绝不修改易商卡原始推送数据（第三方事实底账保持 100% 只读）；
2. **清洗目标**：
   - 历史重复的批量流水（如谢总 80+ 条重复 ChatGPT 订阅）在《个人报销明细》表中建立静默单据，标记为待补发票；
   - **完全跳过飞书卡片消息与待办任务**（不骚扰持卡人）；
   - 通过飞书底层双向关联机制，流水表自动反向关联《个人报销单号》，永久从催报池中剔除，重启服务也绝不会被再次拉取；
3. **样本保留**：每个持卡人、每个典型海外商户场景严格保留 1~2 条真实样本（共 17 条），供后续卡片交互、OCR 识别与 AI 机审全链路联调。

---

## 2. 实施细节与接口扩展

1. **服务能力扩展 (`internal/feishu/card/service.go`)**：
   - 新增 `BatchCreatePrefillDetails(ctx context.Context, items []SilentDetailItem) ([]string, error)` 方法；
   - 适配飞书官方 `POST /records/batch_create` 批量 API，单批次最高 100 条，安全高效；
   - 字段填充规则：
     - `关联流水号`：对应交易流水 Record ID（触发飞书双向关联）；
     - `报销人`：对应持卡人 OpenID；
     - `消费事由`：`[历史流水/待补发票] <商户名称>`；
     - `审核意见`：`历史流水批量建单归档（待补发票）`；
     - `上传发票`：保持为空（无附件）；
     - **不调用 SendCardToUser 与 CreateTask**。
2. **归档工具落地 (`cmd/tools/clean_archive/main.go`)**：
   - 默认 Dry-Run 模式，打印保留与归档全景；
   - 支持 `-execute` 参数受控触发。

---

## 3. 线上清洗验收结果

- **批量静默建单完成**：成功创建 **97 条** 明细记录（谢子豪 91 条、李俊 5 条、宋国杰 1 条）；
- **全量巡检复核 (`./bin/twc-approval-go-bridge scan-transactions`)**：
  - 流水总记录数：152 条
  - 已关联报销明细（已跳过）：**122 条**（原 25 条 + 新归档 97 条）
  - 缺少必填要素在途流水（已过滤）：**13 条**
  - **🎯 最终剩余待催报候选：精确保留 17 条**

---

## 4. 保留继续联调的 17 条样本明细

1. **谢子豪（6 条）**：
   - `OPENAI *CHATGPT SUBSCR SAN FRANCISCO USA` (CNY 85,839.38)
   - `OPENAI* CHATGPT CREDIT SAN FRANCISCO USA` (CNY 4,701.27)
   - `OPENAI SAN FRANCISCO USA` (CNY 4,031.10)
   - `MUSICBED FORT WORTH USA` (CNY 1,346.43，音乐版权)
   - `UNITED HOUSTON USA` (CNY 2,688.44，美联航机票)
   - `Asian Box San Jose USA` (CNY 515.60，海外餐饮)
2. **李俊（6 条）**：
   - `UBR* PENDING.UBER.COM Amsterdam NLD` (CNY 256.88，阿姆斯特丹打车)
   - `UBR* PENDING.UBER.COM SAN FRANCISCO USA` (CNY 156.17，旧金山打车)
   - `LIME*RIDE I5U7 PARIS FRA` (CNY 91.61，巴黎共享滑板车)
   - `AU SOLEIL LEVANT VANVES FRA` (CNY 488.23，餐饮面包)
   - `BRIOCHE DOREE TREMBLAY E FRA` (CNY 262.32，餐饮烘焙)
   - `Wundermart France SA Paris FRA` (CNY 135.71，无人零售)
3. **宋国杰（5 条）**：
   - `STARBUCKS INT ARR SFO SAN FRANCISCO USA` (CNY 121.91，星巴克)
   - `BISCOFF COFFEE CORNER SAN FRANCISCO USA` (CNY 796.74，精品咖啡)
   - `SQ *UME-CA-CUPERTINO Cupertino USA` (CNY 91.13，快餐正餐)
   - `TST*NEW ENGLAND LOBSTE Burlingame USA` (CNY 980.11，龙虾海鲜正餐)
   - `Apple Store #R824 CUPERTINO USA` (CNY 591.00，苹果硬件配件)

---

## 5. 全面触发与常驻自动巡检开启

1. **全面触发派发**：
   - 执行 `./bin/twc-approval-go-bridge scan-transactions -send`；
   - 17 条样本全部完成明细建单、飞书待办创建与 IM 交互卡片投递（谢子豪 6 条、李俊 6 条、宋国杰 5 条）；
   - 多维表格《交易流水表》152 条流水中，139 条全部进入“已关联”状态，13 条在途流水被 5 要素规则过滤，催报池精确定位至 0 条。
2. **常驻后台自动巡检就绪**：
   - `configs/config.toml` 中 `transactions.auto_notify` 设为 `true`；
   - 本地后台守护进程 `com.yingqing.twc-approval-debug` 已平滑重启；
   - 服务每 5 分钟自动巡检一次，一旦有新交易流水写入多维表格，系统将自动识别、过滤并向对应持卡人推送催报卡片。

