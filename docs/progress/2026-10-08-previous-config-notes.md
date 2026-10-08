# 迁移前配置注释归档

2026-10-08。以下内容保留原有注释与模板文档，供历史核对，不再是启动方式；当前配置见[单文件配置](../business-configuration.md)。运行只读取 config.json，示例为 configs/config.example.json。

## 原环境变量示例及注释

```text
# HTTP listener address
HTTP_ADDR=:8080

# Structured log level: debug, info, warn, or error
LOG_LEVEL=info

# Graceful shutdown timeout
SHUTDOWN_TIMEOUT=10s

# Optional Feishu long-connection listener
FEISHU_APP_ID=
FEISHU_APP_SECRET=
FEISHU_EVENT_TYPE=drive.file.bitable_record_changed_v1
FEISHU_LOG_RAW_EVENTS=false

# Select one complete business profile (tables, fields, providers and triggers).
# Leave empty for legacy environment-only configuration below. Credentials stay here.
# Example: BUSINESS_CONFIG_FILE=configs/business/enterprise-test.json
BUSINESS_CONFIG_FILE=

# Legacy business bindings below are ignored when BUSINESS_CONFIG_FILE is set.
# Attachment recognition settings. Anyreceipt is always compiled; IDs remain stable when table or field names change.
RECEIPT_BASE_TOKEN=K5EhbDdEKa8wbJsTCpmcmeVwnHc
RECEIPT_TABLE_ID=tbleThinekEbpK9L
RECEIPT_ATTACHMENT_FIELD_ID=fld9awqaDl
# Set anyreceipt for the standard path, or model when built with Anthropic support.
RECEIPT_PROVIDER=
# event, poll, or both. Polling scans only RECEIPT_BASE_TOKEN / RECEIPT_TABLE_ID.
RECEIPT_TRIGGER_MODE=both
RECEIPT_POLL_INTERVAL=5m
# baseline: first scan only observes existing attachments; process: recognize them.
RECEIPT_POLL_STARTUP=baseline
ANYRECEIPT_API_KEY=
RECEIPT_MODEL_API_KEY=
RECEIPT_MODEL_BASE_URL=https://api.deepseek.com/anthropic
RECEIPT_MODEL_NAME=deepseek-flash

# Set the ledger table ID to write recognized results. Omit it for log-only mode.
RECEIPT_LEDGER_TABLE_ID=tblsCc7P4NRwJytk
RECEIPT_SOURCE_DETAIL_FIELD_ID=fldFR7zXuN
# Semantic field names map to stable Feishu field IDs; renaming columns needs no code change.
RECEIPT_LEDGER_FIELD_IDS={"source_key":"fldDYxci6g","raw_json":"fldFnFXBX2","unique_key":"fldwN62xku","title":"fld7zDhGgQ","invoice_number":"fld2n9chom","receipt_type":"fldvbLnBuR","business_category":"fldthjY1Xq","seller":"fld2W2npaf","buyer":"fld7lWZGzK","currency":"fld3j34HrN","pretax_amount":"fldjKawaXS","tax_amount":"fld6rDFkSs","tax_rate":"fld4908DL5","total_amount":"fld1ZDUvQM","issue_date":"fld23kZeY4","country":"fldazXlajz","ai_summary":"fld6rwXbaf","relation":"fldq9ViHzN"}

# Optional SealAI audit channel. Inject the Bearer token at runtime, never commit it.
SEAL_DOCUMENT_URL=https://mediastorm-test.sealai.cc/api/v1/integrations/webhook/wh_1790692906316_6z2eao6/document
SEAL_BEARER_TOKEN=

# Durable single-host task state. Use a persistent volume in containers.
STATE_DIR=data

# Audit provider is independent of receipt recognition. Seal rules stay in SealAI.
REVIEW_PROVIDER=seal
# manual: explicit submit-review; after_recognition: submit after ledger delivery.
# Automatic mode requires result columns and, for Seal, the configured callback.
REVIEW_TRIGGER_MODE=manual
# Only the self-hosted review provider loads a local rules file.
REVIEW_MODEL_API_KEY=
REVIEW_MODEL_BASE_URL=
REVIEW_MODEL_NAME=
REVIEW_RULES_FILE=configs/review/rules.example.json
# Optional business context and separate AI result columns in the detail table.
REVIEW_CONTEXT_FIELD_IDS={}
REVIEW_RESULT_FIELD_IDS={}
# Optional secret URL token for POST /seal/callback/<token>. Use 32+ random URL-safe characters.
SEAL_CALLBACK_TOKEN=
```

## 原模板说明

# 飞书模板配置

`approval-template.example.json` 是项目编写的审批模板创建请求，按飞书官方协议组织；它不是供应商原文或现有企业模板的导出文件。表单、字段 ID、文案、审批流程和可编辑设置均可在此修改。

使用独立工具 `go run ./cmd/approval-template -app personal -file configs/feishu/approval-template.example.json` 校验；显式加 `-apply` 才创建。凭证由进程环境注入，不写进 JSON。企业租户改用 `-app enterprise`。

完整运行约定与线上验证状态见 [`internal/feishu/approval/README.md`](../../internal/feishu/approval/README.md)，官方原文见 [`external-api/feishu/`](../../external-api/feishu/README.md)。
