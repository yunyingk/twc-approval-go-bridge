package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/yunyingk/twc-approval-go-bridge/internal/config"
	"github.com/yunyingk/twc-approval-go-bridge/internal/feishu/card"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	svc, err := card.NewService(cfg)
	if err != nil {
		log.Fatalf("new card service: %v", err)
	}

	ctx := context.Background()
	rows, fieldMap, transBinding, err := svc.InspectRowsWithSchema(ctx)
	if err != nil {
		log.Fatalf("inspect transaction rows: %v", err)
	}

	fmt.Printf("======================================================================\n")
	fmt.Printf("📊 《交易流水表》线上数据全景分析 (总记录数: %d 条)\n", len(rows))
	fmt.Printf("======================================================================\n\n")

	fmt.Println("=== 线上交易流水表所有字段 (field_id -> field_name) ===")
	for fid, fname := range fieldMap {
		fmt.Printf("  ID: %-16s | Name: %s\n", fid, fname)
	}
	fmt.Println("======================================================================")

	// Inspect reimbursement details table
	detailRows, detailFieldMap, _, err := svc.InspectDetailsWithSchema(ctx)
	if err == nil {
		fmt.Println("\n=== 线上个人报销明细表所有字段 (field_id -> field_name) ===")
		for fid, fname := range detailFieldMap {
			fmt.Printf("  ID: %-16s | Name: %s\n", fid, fname)
		}
		fmt.Printf("====================================================================== (总拉取样本: %d 条)\n", len(detailRows))
		if len(detailRows) > 0 {
			fmt.Println("\n--- 明细表样本记录 1 ---")
			for k, v := range detailRows[0].Fields {
				fmt.Printf("  %s: %v\n", k, v)
			}
		}
	}

	getFieldValue := func(record map[string]any, semantic string) any {
		if transBinding.Fields != nil {
			fieldID := transBinding.Fields[semantic]
			if fieldID != "" {
				fieldName := fieldMap[fieldID]
				if fieldName != "" {
					return record[fieldName]
				}
			}
		}
		return nil
	}

	formatStr := func(v any) string {
		if v == nil {
			return ""
		}
		switch val := v.(type) {
		case string:
			return strings.TrimSpace(val)
		case []any:
			var parts []string
			for _, item := range val {
				if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, ", ")
		default:
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}

	// 5个朱益涛指定的必填字段检查
	var (
		missingBookedAmount int
		missingBookedCurr   int
		missingTxID         int
		missingCardholder   int
		missingTxTime       int
		missingAnyOfFive    int
	)

	// 交易状态统计
	txStatusCounts := make(map[string]int)
	// 报销状态统计
	claimStatusCounts := make(map[string]int)
	// 持卡人统计
	cardholderCounts := make(map[string]int)
	// 已关联个人报销明细数
	alreadyLinkedCount := 0

	// 5个字段都齐全且未关联的记录列表
	var cleanCandidates []map[string]string

	for _, r := range rows {
		fields := r.Fields

		bookedAmt := formatStr(getFieldValue(fields, "booked_amount_cny"))
		if bookedAmt == "" {
			bookedAmt = formatStr(fields["结算金额"])
		}

		bookedCurr := formatStr(fields["结算金额币种"])
		if bookedCurr == "" {
			bookedCurr = formatStr(fields["结算币种"])
		}

		txID := formatStr(getFieldValue(fields, "transaction_id"))
		if txID == "" {
			txID = formatStr(fields["交易流水号"])
		}

		cardholderRaw := getFieldValue(fields, "cardholder")
		if cardholderRaw == nil {
			cardholderRaw = fields["持卡人"]
		}
		_, cardholderName := card.ExtractCardholderOpenID(cardholderRaw)

		txTime := formatStr(getFieldValue(fields, "transaction_time"))
		if txTime == "" {
			txTime = formatStr(fields["交易时间"])
		}

		txStatus := formatStr(getFieldValue(fields, "transaction_status"))
		if txStatus == "" {
			txStatus = formatStr(fields["交易状态"])
		}
		if txStatus == "" {
			txStatus = "（空）"
		}
		txStatusCounts[txStatus]++

		claimStatus := formatStr(getFieldValue(fields, "claim_status"))
		if claimStatus == "" {
			claimStatus = formatStr(fields["报销状态"])
		}
		if claimStatus == "" {
			claimStatus = "（空）"
		}
		claimStatusCounts[claimStatus]++

		if cardholderName == "" {
			cardholderName = "（未识别持卡人）"
		}
		cardholderCounts[cardholderName]++

		// 关联检查
		rel := getFieldValue(fields, "detail_relation")
		if rel == nil {
			rel = fields["个人报销单号"]
		}
		isLinked := card.IsAlreadyLinked(rel)
		if isLinked {
			alreadyLinkedCount++
		}

		// 5字段完整性
		isMissing := false
		if bookedAmt == "" {
			missingBookedAmount++
			isMissing = true
		}
		if bookedCurr == "" {
			missingBookedCurr++
			isMissing = true
		}
		if txID == "" {
			missingTxID++
			isMissing = true
		}
		if cardholderName == "" || cardholderName == "（未识别持卡人）" {
			missingCardholder++
			isMissing = true
		}
		if txTime == "" {
			missingTxTime++
			isMissing = true
		}
		if isMissing {
			missingAnyOfFive++
		} else {
			merchant := formatStr(getFieldValue(fields, "merchant"))
			if merchant == "" {
				merchant = formatStr(fields["商户名称"])
			}
			cleanCandidates = append(cleanCandidates, map[string]string{
				"record_id":    r.RecordID,
				"tx_id":        txID,
				"merchant":     merchant,
				"booked_amt":   bookedAmt,
				"booked_curr":  bookedCurr,
				"cardholder":   cardholderName,
				"tx_time":      txTime,
				"tx_status":    txStatus,
				"claim_status": claimStatus,
				"is_linked":    fmt.Sprintf("%v", isLinked),
			})
		}
	}

	fmt.Println("【一、朱益涛 5 个关键字段缺失统计】")
	fmt.Printf("  1. 结算金额 为空:       %3d 条\n", missingBookedAmount)
	fmt.Printf("  2. 结算金额币种 为空:   %3d 条\n", missingBookedCurr)
	fmt.Printf("  3. 交易流水号 为空:     %3d 条\n", missingTxID)
	fmt.Printf("  4. 持卡人 为空:         %3d 条\n", missingCardholder)
	fmt.Printf("  5. 交易时间 为空:       %3d 条\n", missingTxTime)
	fmt.Printf("  👉 至少缺少上述 1 项的:  %3d 条 (按朱总规则直接过滤，不发！)\n", missingAnyOfFive)
	fmt.Printf("  👉 5 项全部齐全的有效数据: %3d 条\n\n", len(rows)-missingAnyOfFive)

	fmt.Println("【二、交易状态分布】")
	for st, cnt := range txStatusCounts {
		fmt.Printf("  - %-15s: %d 条\n", st, cnt)
	}
	fmt.Println()

	fmt.Println("【三、报销状态分布】")
	for cs, cnt := range claimStatusCounts {
		fmt.Printf("  - %-15s: %d 条\n", cs, cnt)
	}
	fmt.Println()

	fmt.Println("【四、持卡人分布】")
	for ch, cnt := range cardholderCounts {
		fmt.Printf("  - %-15s: %d 条\n", ch, cnt)
	}
	fmt.Println()

	fmt.Printf("【五、已关联个人报销明细】: %d 条\n\n", alreadyLinkedCount)

	// 计算在 5 字段齐全基础上，进一步过滤已关联、失败/撤销后的净有效待报销条数
	var finalCandidates []map[string]string
	for _, c := range cleanCandidates {
		if c["is_linked"] == "true" {
			continue
		}
		if strings.Contains(c["tx_status"], "失败") || strings.Contains(c["tx_status"], "撤销") || strings.Contains(c["tx_status"], "退款") {
			continue
		}
		if strings.Contains(c["claim_status"], "已报销") || strings.Contains(c["claim_status"], "无需报销") {
			continue
		}
		finalCandidates = append(finalCandidates, c)
	}

	fmt.Printf("======================================================================\n")
	fmt.Printf("🎯 清洗后【真正符合条件且需要催报】的最终数据: %d 条\n", len(finalCandidates))
	fmt.Printf("======================================================================\n")

	finalByCardholder := make(map[string]int)
	merchantByCardholder := make(map[string]map[string]int)
	for _, c := range finalCandidates {
		ch := c["cardholder"]
		m := c["merchant"]
		finalByCardholder[ch]++
		if merchantByCardholder[ch] == nil {
			merchantByCardholder[ch] = make(map[string]int)
		}
		merchantByCardholder[ch][m]++
	}
	for ch, cnt := range finalByCardholder {
		fmt.Printf("  - %-15s: %d 条\n", ch, cnt)
		for m, mcnt := range merchantByCardholder[ch] {
			fmt.Printf("      * %-45s : %d 条\n", m, mcnt)
		}
	}
}
