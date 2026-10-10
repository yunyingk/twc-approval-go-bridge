package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/yunyingk/twc-approval-go-bridge/internal/skill"
)

const doubaoSkillAdminURL = "https://admin.doubao.com/ask/doubao/builtin-skill"

// runExportSkill handles the export-skill CLI subcommand.
// By default it exports to "./SKILL.md" which matches Doubao's upload file naming specification.
func runExportSkill(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("export-skill", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outputPath := fs.String("o", "SKILL.md", "导出文件路径 (默认 'SKILL.md'，指定 '-' 则输出到标准输出)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	target := *outputPath
	if target == "-" {
		return skill.ExportToFile("-", output)
	}

	if err := skill.ExportToFile(target, nil); err != nil {
		return fmt.Errorf("导出技能文档失败: %w", err)
	}

	fmt.Fprintf(output, "\n================================================================================\n")
	fmt.Fprintf(output, "✅ 豆包企业内置技能已生成: %s (符合豆包上传规范)\n", target)
	fmt.Fprintf(output, "================================================================================\n")
	fmt.Fprintf(output, "🔗 豆包管理后台上传入口:\n")
	fmt.Fprintf(output, "   %s\n\n", doubaoSkillAdminURL)
	fmt.Fprintf(output, "📋 管理员操作指引:\n")
	fmt.Fprintf(output, "   1. 浏览器打开上方网址（豆包工作 > 技能管理）\n")
	fmt.Fprintf(output, "   2. 点击右上角蓝色【上传】按钮\n")
	fmt.Fprintf(output, "   3. 将生成的【%s】直接拖拽或选中上传\n", target)
	fmt.Fprintf(output, "   4. 上传后默认全员可见，企业成员在“首页 > 市场”添加使用即可\n")
	fmt.Fprintf(output, "================================================================================\n\n")
	return nil
}
