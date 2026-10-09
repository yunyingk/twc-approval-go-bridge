package provision

// TableRole constants
const (
	RoleTransactions         = "transactions"
	RoleReimbursementDetails = "reimbursement_details"
	RoleInvoiceLedger        = "invoice_ledger"
)

// FieldDef defines a standard field in the blueprint schema.
type FieldDef struct {
	SemanticKey string
	Name        string
	Type        int // 1: text, 2: number, 5: date, 7: checkbox, 17: attachment, 18: link
	Property    any
	Source      string // For ledger ordered fields
	Order       int    // For ledger ordered fields
}

// Blueprint defines the canonical schema layout for all three enterprise business tables.
type Blueprint struct {
	TransactionsFields []FieldDef
	DetailsFields      []FieldDef
	DetailsResultCols  []FieldDef
	LedgerOrderedCols  []FieldDef
}

// DefaultBlueprint returns the standard schema structure matching business rules.
func DefaultBlueprint() Blueprint {
	return Blueprint{
		TransactionsFields: []FieldDef{
			{SemanticKey: "transaction_id", Name: "交易流水号", Type: 1},
			{SemanticKey: "cardholder", Name: "持卡人", Type: 1},
			{SemanticKey: "card_last_four", Name: "卡末四位", Type: 1},
			{SemanticKey: "transaction_time", Name: "交易时间", Type: 5},
			{SemanticKey: "merchant", Name: "商户名称", Type: 1},
			{SemanticKey: "original_amount", Name: "交易金额", Type: 2},
			{SemanticKey: "original_currency", Name: "交易币种", Type: 1},
			{SemanticKey: "booked_amount_cny", Name: "结算金额", Type: 2},
			{SemanticKey: "country", Name: "国家/地区", Type: 1},
			{SemanticKey: "transaction_type", Name: "交易类型", Type: 1},
			{SemanticKey: "transaction_status", Name: "交易状态", Type: 1},
			{SemanticKey: "claim_status", Name: "报销状态", Type: 1},
		},
		DetailsFields: []FieldDef{
			{SemanticKey: "detail_id", Name: "明细ID", Type: 1},
			{SemanticKey: "attachment", Name: "发票原件", Type: 17},
			{SemanticKey: "employee", Name: "报销人", Type: 1},
			{SemanticKey: "expense_reason", Name: "费用事由", Type: 1},
			{SemanticKey: "expense_category", Name: "费用分类", Type: 1},
			{SemanticKey: "human_review_status", Name: "人工复核状态", Type: 1},
			{SemanticKey: "human_review_comment", Name: "人工复核意见", Type: 1},
			{SemanticKey: "locked", Name: "锁定", Type: 7},
		},
		DetailsResultCols: []FieldDef{
			{SemanticKey: "decision", Name: "机审结果", Type: 1},
			{SemanticKey: "comment", Name: "机审意见", Type: 1},
			{SemanticKey: "document_id", Name: "审核单号", Type: 1},
			{SemanticKey: "revision", Name: "审核版本", Type: 1},
			{SemanticKey: "provider", Name: "审核渠道", Type: 1},
			{SemanticKey: "external_id", Name: "外部标识", Type: 1},
			{SemanticKey: "url", Name: "报告链接", Type: 1},
		},
		LedgerOrderedCols: []FieldDef{
			{Order: 10, SemanticKey: "ocr_trace_id", Name: "识别流水号", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 20, SemanticKey: "feishu_origin_attachment", Name: "发票原件", Type: 17, Source: "feishu_lookup"},
			// Order 30: feishu_detail_relation is a link field created subsequently
			{Order: 40, SemanticKey: "ocr_seller", Name: "开票方名称", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 50, SemanticKey: "ocr_total_amount", Name: "含税总额", Type: 2, Source: "anyreceipt_ocr"},
			{Order: 60, SemanticKey: "ocr_currency", Name: "币种", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 70, SemanticKey: "ocr_issue_date", Name: "开票日期", Type: 5, Source: "anyreceipt_ocr"},
			{Order: 80, SemanticKey: "bridge_recognition_status", Name: "识别状态", Type: 1, Source: "bridge_computed"},
			{Order: 90, SemanticKey: "ocr_summary", Name: "AI消费概要", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 100, SemanticKey: "ocr_invoice_number", Name: "发票号", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 110, SemanticKey: "ocr_country", Name: "国家", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 120, SemanticKey: "ocr_receipt_type", Name: "票据类型", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 130, SemanticKey: "ocr_business_category", Name: "业务分类", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 140, SemanticKey: "ocr_pretax_amount", Name: "不含税金额", Type: 2, Source: "anyreceipt_ocr"},
			{Order: 150, SemanticKey: "ocr_tax_amount", Name: "税额", Type: 2, Source: "anyreceipt_ocr"},
			{Order: 160, SemanticKey: "ocr_tax_rate", Name: "税率", Type: 2, Source: "anyreceipt_ocr"},
			{Order: 170, SemanticKey: "ocr_title", Name: "发票摘要", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 180, SemanticKey: "ocr_buyer", Name: "购买方名称", Type: 1, Source: "anyreceipt_ocr"},
			{Order: 190, SemanticKey: "bridge_attachment_key", Name: "附件标识", Type: 1, Source: "bridge_computed"},
			{Order: 200, SemanticKey: "bridge_raw_json", Name: "识别原始JSON", Type: 1, Source: "anyreceipt_ocr"},
		},
	}
}
