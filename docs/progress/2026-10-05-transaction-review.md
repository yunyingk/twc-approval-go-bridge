# 2026-10-05：关联交易流水参与审核

用户确认推进「读取关联流水 → 统一审核快照 → 版本化送审」，保持第三方流水只读，Seal 规则继续维护在 SealAI。每阶段独立提交实现、验证和边界。

## 线上发现

项目应用身份只读核验：报销明细的 `fld7GtJqLK` 是类型 21 双向关联，目标为 `tblr0rFnvNntoSYR`，`multiple=false`。实际关联响应是数组，记录 ID 位于 `record_ids`；不能拿显示流水号或 `text` 当作记录 ID。空关联可能仍返回带 `table_id/text_arr` 的对象。

流水金额列为类型 2，但本租户记录读取返回数字字符串；交易时间为毫秒时间戳，币种为单选文本。处理金额须保留十进制精度，缺失和零分别处理。私有字段及少量样本响应保存于忽略的 `data/public-debug/transaction-source-inspection.json`。

## 第一阶段：共同审核契约

- 在 `core/review` 增加独立的交易证据，记录来源、关联记录 ID、真实支付事实和资料质量问题。
- 共用编排把证据送给两个审核提供方，并纳入审核版本；未启用流水时保留旧版本计算。
- Seal 收到完整证据 JSON 及可读文本字段；不把支付金额自动变成发票占用、分摊或汇率。自有审核收到相同证据，存在资料质量问题时直接要求人工复核。
- 契约测试验证大额精度、排序稳定、事实变化、重启复用及旧流水结果不覆盖新数据。

第一阶段提交：`6b47a3c feat: include shared payment evidence in versioned review`。相关默认测试和排除 Anthropic 的契约/共用编排/Seal 测试通过。第一阶段不改变线上配置或新增付费调用。

## 第二阶段：飞书读取与配置

- `review.include_transactions` 显式控制读取，默认关闭保持历史行为；启用时校验同 Base 原生关联、五项必要字段和保留证据键。
- 按稳定字段 ID 与真实关联记录 ID 分页核验结构并读取各条流水；校验实际关联目标，API/配置错误中止准备，不能当作资料缺失。
- 数字字符串和 JSON 数字保留精度；原币与记账 CNY 金额独立，支持零和有符号金额，不猜汇率或分摊。空关联、无效金额/币种/时间明确标注，问题原值保留。
- `preview-review` 使用实际准备入口与版本计算，输出业务快照而不提交审核、调用 OCR 或写记录；不输出原件二进制与临时链接。
- 新增来源测试覆盖原生空关联、稳定字段改名、真实记录定位、读权限失败、关联目标错误、多关联、精确金额及格式异常。

线上已核验的带流水明细与已有 OCR 测试台账暂不重合；优先分别验证真实关联读取及已有票据快照，避免为补测试而修改第三方流水或重复识别。真实只读验收和启用结果随后记录。

第二阶段提交：`84c46cd feat: read configured Feishu payment links for audit snapshots`。`go test ./...`、`go vet ./...`、`go test -tags no_anthropic ./...` 和两种构建通过；补充的币种/时间原值保留测试也通过。

## 第三阶段：真实读取、预览与启用

- 读取真实明细 `reczz28HBeFn4GdB` 的关联流水，得到 1 条有效支付记录、0 个资料质量问题。金额与源数字字符串完全一致，币种匹配，生成 11 个 Seal 文本证据字段。没有调用 Seal 送审，也没有修改明细或流水。
- 对已完成 OCR 的两份测试明细 `reczz28JNpKh1Mk4`、`reczz28JGJx5y6y8` 执行完整 `preview-review`，均正确读取 1 张有效台账票据；两行原本未关联流水，均显式输出 `missing_transaction_relation`，没有把它们与别人的支付记录拼接。
- 在已选择的企业 JSON 中启用 `include_transactions=true`。新旧识别参数不变，启动 `public-debug-20261005-transaction-review`；启动日志确认开关及 `after_recognition`，WebSocket 已重新连接。本地与公网健康、就绪、版本均返回 200。
- 使用本机正式运行包装器再次预览已有票据成功。全程 8 个持久化业务 JSON 文件内容保持一致，发票台账仍 7 行，Anyreceipt 余额仍为 206；没有产生额外识别或审核任务。

只读证据分别保存在忽略的 `data/public-debug/transaction-review-live-evidence.json`、`transaction-review-preview-<record-id>.json`、`transaction-review-runtime-preview.json`、`transaction-review-validation-state.json` 和 `transaction-review-runtime-verification.json`。私有读取诊断源代码留于忽略的 `tmp/_transaction-preview/`，以免临时程序被 `go test ./...` 当作正式包扫描。

## 边界与后续

本次已验证真实流水读取、实际审核准备入口和供应商契约转换；没有新增「带流水的 Seal 付费送审与真实回调」样本，自有审核只做受控接口测试。现有 Seal 通道的规则发布状态不由桥接代码推断，也未修改规则。

本次不把金额差异、跨币种、退款或多笔支付自行判为审批结论；没有发票占用、费用分摊或汇率推导。流水变更会使回写前的版本核验不一致，但暂不自动产生新审核；用户选择的自动触发仍是识别完成。没有新增业务冻结锁，多次读取和回写之间仍存在并发窗口。下一步优先处理修改、撤回重提与飞书人工审批闭环。
