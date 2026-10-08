# 2026-10-08 配置结构化收敛与无用兼容层清理

## 背景与目标

在完成多维表格 Schema 拆分与 SealAI 配置简化后，主配置模型 `config.Config` 仍残留大量历史平铺地层：
1. **平铺冗余字段**：`Config` 结构体平铺了 39 个字段，且将 `BusinessProfile` 手动通过 `apply()` 复制了一遍；
2. **多余胶水代码**：`apply()` 胶水代码和重复的 `ReceiptModel*` / `ReviewModel*` 映射；
3. **过时文案**：`cmd/server/main.go` 中残存 `unsupported RECEIPT_PROVIDER` 环境变量时代旧报错；
4. **自描述性缺口**：主配置 TOML 中未声明 `rules_file` 说明与示例；
5. **文档滞后**：`README.md` 中的配置路径及段落与当前实现脱节。

本轮目标彻底移除无用的过渡兼容层，将 `Config` 彻底收敛为结构化层次，并补齐文档与路径测试。

---

## 核心实施内容

### 1. 结构化 Config 收敛与无用兼容层清理
- `internal/config/config.go`：
  - 新增 `RuntimeConfig`，将运行参数收敛到结构内；
  - 精简 `Config` 为结构化组合：
    - `ConfigFile`、`TablesFile`；
    - `Runtime` (`RuntimeConfig`)；
    - `Feishu` (`FeishuSettings`)；
    - `Anyreceipt` (`AnyreceiptSettings`)；
    - `Seal` (`SealConfig`)；
    - `Model` (`ModelSettings` - 共享模型段)；
    - `Business` (`*BusinessProfile` - 直接持有完整业务拓扑)；
    - `ReceiptPollInterval`、`ReceiptPollStartup`。
  - 完全删除 `BusinessProfile.apply(&cfg)` 胶水方法及 39 个平铺搬运字段。
  - 彻底撤销未上线的过渡兼容代码（移除根目录 `config.toml` 回退读取，默认直接精准定位 `configs/config.toml`）。

### 2. 消费方适配
- `cmd/server/`：
  - `main.go`、`review.go`、`inspect_review.go`、`check_business.go`、`model_enabled.go`、`review_source_events.go`：
    - 全面适配结构化访问：通过 `cfg.Runtime`、`cfg.Feishu`、`cfg.Seal`、`cfg.Model` 以及 `cfg.Business.Tables` 直接获取配置；
    - 修复 `main.go` 第 142 行的过时错误文案，精准输出 `unsupported receipt recognition provider %q`。

### 3. 主配置与自描述性对齐
- `configs/config.example.toml` & `configs/config.toml`：
  - 在 `[review]` 节补充 `rules_file = "configs/rules/review.example.json"` 注释及相对路径查找说明；
- `README.md`：
  - 修正配置文件路径为 `configs/config.toml`；
  - 修正配置说明，指出业务表在 `tables_file`，模型在全局共享 `[model]` 段，模型审核规则由 `[review] rules_file` 独立挂载。

### 4. 双通道相对路径解析与验证
- 在 `internal/config/config.go` 明确注释 `tables_file` 相对路径双通道机制（优先 CWD，兜底主 TOML 目录）；
- 在 `internal/config/config_test.go` 新增 `TestTablesFileDualChannelResolution` 单元测试，保证无论从仓库根目录还是配置子目录启动均能稳健加载。

---

## 验证与验收

1. **测试回归**：
   - `go test ./...` 全绿；
   - `go test -tags no_anthropic ./...` 全绿；
   - `go vet ./...` 检查通过。
2. **线上业务配置核验**：
   - 运行 `./bin/twc-approval-go-bridge check-business-config`，成功以应用身份只读核验三张表（交易流水表 14 字段、个人报销明细表 20 字段、发票台账表 18 字段），全部成功通过。
