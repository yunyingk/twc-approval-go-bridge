package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/yunyingk/twc-approval-go-bridge/internal/skill"
)

// runExportSkill handles the export-skill CLI subcommand.
func runExportSkill(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("export-skill", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outputPath := fs.String("o", "", "导出文件路径 (留空或 '-' 则输出到标准输出)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	target := *outputPath
	if target == "" || target == "-" {
		return skill.ExportToFile("-", output)
	}

	if err := skill.ExportToFile(target, nil); err != nil {
		return fmt.Errorf("导出技能文档失败: %w", err)
	}

	fmt.Fprintf(output, "✅ 豆包企业内置技能已成功导出至: %s\n", target)
	fmt.Fprintf(output, "👉 管理员可前往 豆包企业管理后台 (https://admin.doubao.com/ask/doubao/builtin-skill) 点击【上传】导入该配置。\n")
	return nil
}
