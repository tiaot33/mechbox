package main

// 变体 C「待办流」——没有持久的目录视图,只有一条「需要你处理什么」的流。
// 健康的东西折叠成一行计数,异常的排在最上面。主要可供性是「处理问题」,
// 浏览目录降级为次要动作(真做的话交给命令面板 / 搜索)。

import (
	"fmt"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library"
)

// attentionItem 是流里的一条:一个需要处理的投影,或一个无投影的技能。
type attentionItem struct {
	headline string
	detail   string
	action   string
	glyph    string
	severity int
}

func (m model) attentionItems() []attentionItem {
	var items []attentionItem
	for _, insp := range m.snap.inspections {
		name := m.snap.skillNames[insp.SkillID]
		label := "未知作用域"
		for _, col := range m.snap.scopes {
			if sameScope(insp.Scope, col.scope) {
				label = col.label
			}
		}
		switch insp.Status {
		case library.ActivationDirectoryMissing:
			items = append(items, attentionItem{
				glyph: statusGlyph(insp.Status), severity: 4,
				headline: fmt.Sprintf("%s 的作用域目录不在了", label),
				detail:   fmt.Sprintf("「%s」记录指向 %s", name, insp.Directory),
				action:   "重建目录后重新激活,或撤销这条投影",
			})
		case library.ActivationMissing:
			items = append(items, attentionItem{
				glyph: statusGlyph(insp.Status), severity: 4,
				headline: fmt.Sprintf("「%s」在 %s 的文件夹被删了", name, label),
				detail:   insp.Directory,
				action:   "重新激活即可补回",
			})
		case library.ActivationModified:
			items = append(items, attentionItem{
				glyph: statusGlyph(insp.Status), severity: 3,
				headline: fmt.Sprintf("「%s」在 %s 被就地改过", name, label),
				detail:   "磁盘内容与库里任何版本都不一致",
				action:   "重新激活会覆盖这些改动",
			})
		case library.ActivationStale:
			items = append(items, attentionItem{
				glyph: statusGlyph(insp.Status), severity: 2,
				headline: fmt.Sprintf("「%s」在 %s 还停在旧版本", name, label),
				detail:   "磁盘内容与某个历史版本一致",
				action:   "重新激活刷新到当前版本",
			})
		}
	}

	// 库里有、但一个作用域都没投影的技能:不是错误,但值得提一句。
	for _, s := range m.snap.skills {
		active := false
		for _, col := range m.snap.scopes {
			if _, ok := m.snap.bySkillScope[scopeKey(s.ID, col.label)]; ok {
				active = true
				break
			}
		}
		if !active {
			items = append(items, attentionItem{
				glyph: dimSt.Render("○"), severity: 1,
				headline: fmt.Sprintf("「%s」装了但没在任何地方启用", s.Name),
				detail:   s.Description,
				action:   "选一个作用域激活",
			})
		}
	}

	// 严重的排前面,同级保持原顺序。
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].severity > items[j-1].severity; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	return items
}

func (m model) viewC() string {
	items := m.attentionItems()
	var healthy int
	for _, insp := range m.snap.inspections {
		if insp.Status == library.ActivationCurrent {
			healthy++
		}
	}

	var b strings.Builder
	b.WriteString(titleSt.Render("需要处理") + dimSt.Render(fmt.Sprintf("  %d 项", len(items))) + "\n")
	b.WriteString(dimSt.Render(fmt.Sprintf("另有 %d 条投影与当前版本一致,已折叠", healthy)) + "\n\n")

	if len(items) == 0 {
		b.WriteString(dimSt.Render("  一切正常。\n"))
	}
	for i, it := range items {
		marker := "  "
		head := it.headline
		if i == m.cursor {
			marker = "▸ "
			head = selSt.Render(" " + head + " ")
		}
		b.WriteString(marker + it.glyph + " " + head + "\n")
		b.WriteString("     " + dimSt.Render(truncate(it.detail, 72)) + "\n")
		if i == m.cursor {
			b.WriteString("     " + sectionSt.Render("→ "+it.action) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(dimSt.Render(strings.Repeat("─", 60)) + "\n")
	b.WriteString(dimSt.Render(fmt.Sprintf(
		"库里 %d 个技能 · %d 个分组 · %d 个已登记项目 · %d 条投影记录",
		len(m.snap.skills), len(m.snap.groups), len(m.snap.projects), len(m.snap.inspections))))
	return b.String()
}
