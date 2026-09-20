package main

// 变体 A「双栏浏览」——最保守的一版:左边技能清单,右边选中项详情。
// 主要可供性是「浏览目录」,激活状态只是详情里的一个附属区块。

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tiaot33/skills-tui/internal/library"
)

func (m model) viewA() string {
	var left strings.Builder
	left.WriteString(titleSt.Render("技能") + dimSt.Render(fmt.Sprintf("  %d 项", len(m.snap.skills))) + "\n\n")
	for i, s := range m.snap.skills {
		// 行尾缀一个最严重的投影状态,让清单不至于完全看不到健康度。
		line := fit(s.Name, 26) + " " + m.worstGlyph(s.ID)
		if i == m.cursor {
			left.WriteString(selSt.Render(" "+line+" ") + "\n")
		} else {
			left.WriteString(" " + line + "\n")
		}
	}

	var right strings.Builder
	if len(m.snap.skills) > 0 {
		s := m.snap.skills[m.cursor]
		right.WriteString(headerSt.Render(s.Name) + "\n")
		right.WriteString(dimSt.Render(wrap(s.Description, 46)) + "\n\n")

		right.WriteString(sectionSt.Render("来源") + "\n")
		right.WriteString(fmt.Sprintf("  类型  %s\n", s.Source.Type))
		right.WriteString(fmt.Sprintf("  位置  %s\n", dimSt.Render(truncate(sourceLabel(s.Source), 36))))
		right.WriteString(fmt.Sprintf("  装入  %s\n", stamp(s.InstalledAt)))
		right.WriteString(fmt.Sprintf("  更新  %s\n\n", stamp(s.UpdatedAt)))

		right.WriteString(sectionSt.Render("投影") + "\n")
		found := false
		for _, col := range m.snap.scopes {
			insp, ok := m.snap.bySkillScope[scopeKey(s.ID, col.label)]
			if !ok {
				continue
			}
			found = true
			right.WriteString("  " + statusGlyph(insp.Status) + " " + fit(col.label, 22) + " " +
				dimSt.Render(statusText(insp.Status)) + "\n")
			right.WriteString("    " + dimSt.Render(by(insp, m.snap.groupNames)) + "\n")
		}
		if !found {
			right.WriteString(dimSt.Render("  尚未激活到任何作用域\n"))
		}
	}

	leftPane := paneSt.Width(36).Render(left.String())
	rightPane := paneSt.Width(50).Render(right.String())
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
}

// worstGlyph 取该技能所有投影里最该被注意的一个状态。
func (m model) worstGlyph(id library.SkillID) string {
	rank := map[library.ActivationStatus]int{
		library.ActivationDirectoryMissing: 4,
		library.ActivationMissing:          4,
		library.ActivationModified:         3,
		library.ActivationStale:            2,
		library.ActivationCurrent:          1,
	}
	var worst library.ActivationStatus
	best := 0
	for _, col := range m.snap.scopes {
		if insp, ok := m.snap.bySkillScope[scopeKey(id, col.label)]; ok && rank[insp.Status] > best {
			best, worst = rank[insp.Status], insp.Status
		}
	}
	if best == 0 {
		return dimSt.Render("·")
	}
	return statusGlyph(worst)
}

func by(insp library.ActivationInspection, names map[library.GroupID]string) string {
	var parts []string
	if insp.Individual {
		parts = append(parts, "独立激活")
	}
	for _, g := range insp.ViaGroups {
		parts = append(parts, "经分组「"+names[g]+"」")
	}
	if len(parts) == 0 {
		return "无依据(异常)"
	}
	return strings.Join(parts, " + ")
}

func sourceLabel(s library.Source) string {
	switch {
	case s.Locator.Repository != "":
		return s.Locator.Repository
	case s.Locator.URL != "":
		return s.Locator.URL
	case s.Locator.Path != "":
		return s.Locator.Path
	}
	return "—"
}
