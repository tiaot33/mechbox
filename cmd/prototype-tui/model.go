package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tiaot33/skills-tui/internal/library"
)

// scopeRow 是矩阵里的一列 / 详情里的一行:一个 Scope 身份加上它的显示名。
type scopeRow struct {
	scope library.Scope
	label string
	short string
}

// snapshot 是一次性读出的全部展示数据。原型不做增量刷新,按 r 整体重读。
type snapshot struct {
	skills      []library.SkillSummary
	groups      []library.Group
	groupNames  map[library.GroupID]string
	projects    []library.RegisteredProject
	agents      []library.Agent
	scopes      []scopeRow
	inspections []library.ActivationInspection
	// bySkillScope 用 "skillID/scopeLabel" 索引投影状态,矩阵靠它查格子。
	bySkillScope map[string]library.ActivationInspection
	skillNames   map[library.SkillID]string
}

func scopeKey(id library.SkillID, label string) string {
	return fmt.Sprintf("%d/%s", id, label)
}

func loadSnapshot(ctx context.Context, env *seedEnv) (snapshot, error) {
	lib := env.lib
	var s snapshot
	var err error
	if s.skills, err = lib.ListSkills(ctx); err != nil {
		return s, err
	}
	if s.groups, err = lib.ListGroups(ctx); err != nil {
		return s, err
	}
	if s.projects, err = lib.ListRegisteredProjects(ctx); err != nil {
		return s, err
	}
	if s.agents, err = lib.ListAgents(ctx); err != nil {
		return s, err
	}
	if s.inspections, err = lib.InspectActivations(ctx); err != nil {
		return s, err
	}

	s.groupNames = map[library.GroupID]string{}
	for _, g := range s.groups {
		s.groupNames[g.ID] = g.Name
	}
	s.skillNames = map[library.SkillID]string{}
	for _, k := range s.skills {
		s.skillNames[k.ID] = k.Name
	}

	// Scope 列:每个 Agent 的全局,再乘每个已登记项目。
	for _, agent := range s.agents {
		s.scopes = append(s.scopes, scopeRow{
			scope: library.GlobalScope{Agent: agent.Code},
			label: agent.Name + " · 全局",
			short: shortAgent(agent.Code) + "/全局",
		})
	}
	for _, p := range s.projects {
		base := p.Path[strings.LastIndex(p.Path, "/")+1:]
		for _, agent := range s.agents {
			s.scopes = append(s.scopes, scopeRow{
				scope: library.ProjectScope{Agent: agent.Code, ProjectID: p.ID},
				label: agent.Name + " · " + base,
				short: shortAgent(agent.Code) + "/" + base,
			})
		}
	}

	s.bySkillScope = map[string]library.ActivationInspection{}
	for _, insp := range s.inspections {
		for _, col := range s.scopes {
			if sameScope(insp.Scope, col.scope) {
				s.bySkillScope[scopeKey(insp.SkillID, col.label)] = insp
			}
		}
	}
	sort.SliceStable(s.skills, func(i, j int) bool { return s.skills[i].Name < s.skills[j].Name })
	return s, nil
}

func shortAgent(code library.AgentCode) string {
	if code == library.AgentClaudeCode {
		return "CC"
	}
	return "CX"
}

// sameScope 比较两个 Scope 身份。Scope 是密封接口,原型直接按具体类型比。
func sameScope(a, b library.Scope) bool {
	switch x := a.(type) {
	case library.GlobalScope:
		y, ok := b.(library.GlobalScope)
		return ok && x.Agent == y.Agent
	case library.ProjectScope:
		y, ok := b.(library.ProjectScope)
		return ok && x.Agent == y.Agent && x.ProjectID == y.ProjectID
	}
	return false
}

// ---- 样式 ----

var (
	accent    = lipgloss.Color("39")
	dim       = lipgloss.Color("244")
	warn      = lipgloss.Color("214")
	bad       = lipgloss.Color("203")
	good      = lipgloss.Color("78")
	titleSt   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimSt     = lipgloss.NewStyle().Foreground(dim)
	selSt     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(accent)
	barSt     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("221")).Padding(0, 1)
	paneSt    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(dim).Padding(0, 1)
	headerSt  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	sectionSt = lipgloss.NewStyle().Bold(true).Foreground(warn)
)

// statusGlyph 把投影状态映射成一个带色的字形。空状态表示该格没有投影。
func statusGlyph(st library.ActivationStatus) string {
	switch st {
	case library.ActivationCurrent:
		return lipgloss.NewStyle().Foreground(good).Render("●")
	case library.ActivationStale:
		return lipgloss.NewStyle().Foreground(warn).Render("◐")
	case library.ActivationModified:
		return lipgloss.NewStyle().Foreground(warn).Render("✎")
	case library.ActivationMissing:
		return lipgloss.NewStyle().Foreground(bad).Render("✗")
	case library.ActivationDirectoryMissing:
		return lipgloss.NewStyle().Foreground(bad).Render("⌫")
	}
	return dimSt.Render("·")
}

func statusText(st library.ActivationStatus) string {
	switch st {
	case library.ActivationCurrent:
		return "与当前版本一致"
	case library.ActivationStale:
		return "停留在旧版本"
	case library.ActivationModified:
		return "磁盘上被改过"
	case library.ActivationMissing:
		return "文件夹不见了"
	case library.ActivationDirectoryMissing:
		return "Scope 目录不存在"
	}
	return "未激活"
}

// ---- 根模型 ----

type variant int

const (
	variantA variant = iota
	variantB
	variantC
	variantCount
)

var variantNames = [variantCount]string{"双栏浏览", "激活矩阵", "待办流"}

type model struct {
	ctx      context.Context
	env      *seedEnv
	snap     snapshot
	current  variant
	cursor   int
	col      int // 矩阵用的列光标
	width    int
	height   int
	quitting bool
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "right", "l":
			m.current = (m.current + 1) % variantCount
			m.cursor, m.col = 0, 0
		case "left", "h":
			m.current = (m.current + variantCount - 1) % variantCount
			m.cursor, m.col = 0, 0
		case "down", "j":
			m.cursor++
		case "up", "k":
			m.cursor--
		case "tab":
			m.col++
		case "shift+tab":
			m.col--
		case "r":
			if snap, err := loadSnapshot(m.ctx, m.env); err == nil {
				m.snap = snap
			}
		}
	}
	m.cursor = clamp(m.cursor, 0, m.rowCount()-1)
	m.col = clamp(m.col, 0, len(m.snap.scopes)-1)
	return m, nil
}

func (m model) rowCount() int {
	switch m.current {
	case variantC:
		return len(m.attentionItems())
	default:
		return len(m.snap.skills)
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	var body string
	switch m.current {
	case variantA:
		body = m.viewA()
	case variantB:
		body = m.viewB()
	case variantC:
		body = m.viewC()
	}
	return body + "\n" + m.switcher()
}

// switcher 是底部的变体切换条。刻意做成高对比的一条,让人一眼看出它不属于被评估的设计。
func (m model) switcher() string {
	label := fmt.Sprintf("◀ ←/→  变体 %d/%d:%s  ▶", int(m.current)+1, int(variantCount), variantNames[m.current])
	help := dimSt.Render("  ↑/↓ 选择   tab 换列(矩阵)   r 重读   q 退出")
	return barSt.Render(label) + help
}
