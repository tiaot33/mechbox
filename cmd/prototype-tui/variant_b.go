package main

// 变体 B「激活矩阵」——行是技能,列是 Scope,格子是投影状态。
// 主要可供性是「在哪儿开着」,而不是「库里有什么」;信息层级完全围绕 Scope 展开,
// 技能的描述、来源、版本在这一版里根本不出现。

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m model) viewB() string {
	var b strings.Builder
	b.WriteString(titleSt.Render("激活矩阵") +
		dimSt.Render(fmt.Sprintf("  %d 技能 × %d 作用域", len(m.snap.skills), len(m.snap.scopes))) + "\n\n")

	// 表头:列名竖着放不下,用短代号,底下给图例。
	b.WriteString(strings.Repeat(" ", 28))
	for i, col := range m.snap.scopes {
		cell := fit(col.short, 11) + " "
		if i == m.col {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(accent).Render(cell))
		} else {
			b.WriteString(dimSt.Render(cell))
		}
	}
	b.WriteString("\n")

	for i, s := range m.snap.skills {
		name := " " + fit(s.Name, 26) + " "
		if i == m.cursor {
			b.WriteString(selSt.Render(name))
		} else {
			b.WriteString(name)
		}
		for j, col := range m.snap.scopes {
			insp, ok := m.snap.bySkillScope[scopeKey(s.ID, col.label)]
			glyph := dimSt.Render("·")
			if ok {
				glyph = statusGlyph(insp.Status)
			}
			// 选中格用方括号框住,两侧各占一列,保证整行不因选中而错位。
			lead, trail := "  ", "         "
			if i == m.cursor && j == m.col {
				st := lipgloss.NewStyle().Foreground(accent)
				lead, trail = " "+st.Render("["), st.Render("]")+"        "
			}
			b.WriteString(lead + glyph + trail)
		}
		b.WriteString("\n")
	}

	// 选中格的说明:矩阵本身只能给一个字形,细节放在下面一行。
	b.WriteString("\n")
	if len(m.snap.skills) > 0 && len(m.snap.scopes) > 0 {
		s := m.snap.skills[m.cursor]
		col := m.snap.scopes[m.col]
		b.WriteString(headerSt.Render(s.Name) + dimSt.Render("  在  ") + headerSt.Render(col.label) + "\n")
		if insp, ok := m.snap.bySkillScope[scopeKey(s.ID, col.label)]; ok {
			b.WriteString(fmt.Sprintf("  %s %s   %s\n", statusGlyph(insp.Status), statusText(insp.Status),
				dimSt.Render(by(insp, m.snap.groupNames))))
			b.WriteString("  " + dimSt.Render(insp.Directory) + "\n")
		} else {
			b.WriteString(dimSt.Render("  未激活 —— 回车可投影到这个作用域(原型不执行写操作)\n"))
		}
	}

	b.WriteString("\n" + dimSt.Render("图例  ") +
		statusGlyph("current") + dimSt.Render(" 一致  ") +
		statusGlyph("stale") + dimSt.Render(" 旧版  ") +
		statusGlyph("modified") + dimSt.Render(" 被改  ") +
		statusGlyph("missing") + dimSt.Render(" 丢失  ") +
		statusGlyph("directory_missing") + dimSt.Render(" 目录没了  ") +
		dimSt.Render("· 未激活"))
	return b.String()
}
