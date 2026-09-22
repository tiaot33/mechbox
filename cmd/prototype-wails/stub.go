package main

// PROTOTYPE 内存桩:形状照搬 library 门面(类型直接复用),行为只模拟 UI 需要看到的规则:
// 独立激活与分组来源分开记录、仅分组激活的技能不能单独停用、停用分组只移除该组来源、
// 加入分组不会自动投影。所有状态只在内存里,重启即复原。

import (
	"fmt"
	"log"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// ScopeRef 是 Scope 的可序列化形态;ProjectID 为 0 表示 Agent 的全局级别。
type ScopeRef struct {
	Agent     library.AgentCode `json:"agent"`
	ProjectID library.ProjectID `json:"project_id,omitempty"`
}

type ActivationView struct {
	SkillID     library.SkillID          `json:"skill_id"`
	Scope       ScopeRef                 `json:"scope"`
	Directory   string                   `json:"directory"`
	Individual  bool                     `json:"individual"`
	ViaGroups   []library.GroupID        `json:"via_groups"`
	ActivatedAt time.Time                `json:"activated_at"`
	Status      library.ActivationStatus `json:"status"`
}

type GroupView struct {
	library.Group
	Members []library.SkillID `json:"members"`
}

type SkillView struct {
	library.SkillSummary
	UpdateAvailable bool `json:"update_available"`
	Versions        int  `json:"versions"`
}

type Snapshot struct {
	Agents      []library.Agent             `json:"agents"`
	Projects    []library.RegisteredProject `json:"projects"`
	Skills      []SkillView                 `json:"skills"`
	Collections []library.Collection        `json:"collections"`
	Groups      []GroupView                 `json:"groups"`
	Activations []ActivationView            `json:"activations"`
}

type SkillDetail struct {
	Skill    library.Skill          `json:"skill"`
	Versions []library.SkillVersion `json:"versions"`
}

type App struct {
	mu          sync.Mutex
	agents      []library.Agent
	projects    []library.RegisteredProject
	skills      []*SkillView
	files       map[library.SkillID][]library.File
	versions    map[library.SkillID][]library.SkillVersion
	groups      []*GroupView
	activations []*ActivationView
}

var now = time.Date(2026, 9, 23, 10, 0, 0, 0, time.Local)

func daysAgo(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

func NewApp() *App {
	a := &App{
		agents: []library.Agent{
			{Code: library.AgentClaudeCode, Name: "Claude Code"},
			{Code: library.AgentCodex, Name: "Codex"},
		},
		projects: []library.RegisteredProject{
			{ID: 1, Path: "/Users/dur/code/skills-app"},
			{ID: 2, Path: "/Users/dur/code/blog"},
			{ID: 3, Path: "/Users/dur/work/billing-service"},
		},
		files:    map[library.SkillID][]library.File{},
		versions: map[library.SkillID][]library.SkillVersion{},
	}
	a.seed()
	return a
}

type seedSkill struct {
	name, desc string
	extra      []string // SKILL.md 之外的文件
	installed  int      // 几天前安装
	versions   int
	update     bool
}

func (a *App) seed() {
	sources := []struct {
		source library.Source
		skills []seedSkill
	}{
		{library.Source{ID: 1, Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: "https://github.com/anthropics/skills", Branch: "main"}, LastFetched: daysAgo(2), LastSeenRef: "4f2a9c1"}, []seedSkill{
			{"pdf", "Extract text and tables from PDFs, fill forms, merge and split documents.", []string{"scripts/fill_form.py", "reference.md"}, 90, 3, false},
			{"docx", "Create, edit and review Word documents with tracked changes and comments.", []string{"ooxml.md"}, 90, 2, true},
			{"xlsx", "Build spreadsheets with formulas, formatting and charts; analyse tabular data.", []string{"recalc.py"}, 90, 2, false},
			{"skill-creator", "Guide for creating effective skills that extend an agent's capabilities.", []string{"scripts/init_skill.py"}, 60, 4, true},
			{"webapp-testing", "Test local web applications with Playwright: screenshots, console logs, flows.", []string{"scripts/with_server.py"}, 45, 1, false},
			{"frontend-design", "Create distinctive, production-grade frontend interfaces that avoid generic AI aesthetics.", nil, 30, 2, false},
		}},
		{library.Source{ID: 2, Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: "https://github.com/mattpocock/skills", Branch: "main"}, LastFetched: daysAgo(1), LastSeenRef: "b81e0d7"}, []seedSkill{
			{"grill-me", "A relentless interview to sharpen a plan or design before any code is written.", nil, 40, 3, false},
			{"tdd", "Red-green-refactor loop: write one failing test, make it pass, then clean up.", nil, 40, 2, false},
			{"handoff", "Compact the current conversation into a handoff note for the next session.", nil, 38, 1, false},
			{"domain-modeling", "Build and sharpen a project's domain model and ubiquitous language.", []string{"CONTEXT-template.md"}, 20, 2, true},
			{"prototype", "Build throwaway prototypes that answer a design or logic question fast.", []string{"UI.md", "LOGIC.md"}, 10, 1, false},
			{"write-a-prd", "Turn a rough idea into a PRD through a structured interview.", nil, 20, 1, false},
		}},
		{library.Source{ID: 3, Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: "https://github.com/vercel-labs/agent-skills", Branch: "v1.4.0"}, LastFetched: daysAgo(12), LastSeenRef: "9d03a55"}, []seedSkill{
			{"react-best-practices", "React and Next.js performance rules ranked by impact.", []string{"rules/"}, 25, 1, false},
			{"web-design-guidelines", "Review UI code against web interface guidelines for accessibility and polish.", nil, 25, 1, false},
		}},
		{library.Source{ID: 4, Type: library.SourceStore, Locator: library.SourceLocator{PackageID: "vercel-labs/find-skills", Version: "1.2.0"}, LastFetched: daysAgo(5)}, []seedSkill{
			{"find-skills", "Helps users discover and install agent skills from the open ecosystem.", nil, 15, 2, false},
		}},
		{library.Source{ID: 5, Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://downloads.firecrawl.dev/skills/firecrawl-search.zip"}, LastFetched: daysAgo(8), LastSeenRef: "sha256:77c1…"}, []seedSkill{
			{"firecrawl-search", "Web search with full page content extraction via the Firecrawl API.", []string{"scripts/search.sh"}, 8, 1, false},
		}},
		{library.Source{ID: 6, Type: library.SourceLocal, Locator: library.SourceLocator{Path: "/Users/dur/code/my-skills"}}, []seedSkill{
			{"release-notes", "Draft release notes from merged PRs since the last tag.", nil, 70, 5, false},
			{"db-migrate-check", "Review a SQL migration for locking, backfill and rollback hazards.", []string{"checklist.md"}, 50, 3, false},
			{"ask-dur", "Route a question to the right in-house skill or runbook.", nil, 3, 1, false},
		}},
	}

	var id library.SkillID
	var versionID library.VersionID
	for _, s := range sources {
		for _, sk := range s.skills {
			id++
			installed := daysAgo(sk.installed)
			updated := installed
			if sk.versions > 1 {
				updated = daysAgo(sk.installed / sk.versions)
			}
			a.skills = append(a.skills, &SkillView{
				SkillSummary: library.SkillSummary{
					ID: id, Name: sk.name, Description: sk.desc, Source: s.source,
					InstalledAt: installed, UpdatedAt: updated,
				},
				UpdateAvailable: sk.update,
				Versions:        sk.versions,
			})
			files := []library.File{{Path: "SKILL.md", Mode: library.ModeRegular, Content: []byte(skillMarkdown(sk.name, sk.desc))}}
			for _, f := range sk.extra {
				mode := library.ModeRegular
				if strings.HasSuffix(f, ".py") || strings.HasSuffix(f, ".sh") {
					mode = library.ModeExecutable
				}
				files = append(files, library.File{Path: f, Mode: mode, Content: []byte("# " + f + "\n")})
			}
			a.files[id] = files
			for v := range sk.versions {
				versionID++
				at := installed.Add(time.Duration(v) * updated.Sub(installed) / time.Duration(max(sk.versions-1, 1)))
				a.versions[id] = append(a.versions[id], library.SkillVersion{
					ID: versionID, SkillID: id, Description: sk.desc, CreatedAt: at,
					GitTreeSHA: fmt.Sprintf("%07x", uint32(versionID)*2654435761%0xfffffff),
				})
			}
		}
	}

	byName := func(names ...string) []library.SkillID {
		var ids []library.SkillID
		for _, n := range names {
			ids = append(ids, a.skillByName(n).ID)
		}
		return ids
	}
	a.groups = []*GroupView{
		{library.Group{ID: 1, Name: "规划与需求", CreatedAt: daysAgo(30)}, byName("grill-me", "write-a-prd", "domain-modeling", "prototype")},
		{library.Group{ID: 2, Name: "前端调试", CreatedAt: daysAgo(25)}, byName("webapp-testing", "frontend-design", "react-best-practices", "web-design-guidelines")},
		{library.Group{ID: 3, Name: "文档处理", CreatedAt: daysAgo(20)}, byName("pdf", "docx", "xlsx")},
		{library.Group{ID: 4, Name: "发布流程", CreatedAt: daysAgo(12)}, byName("release-notes", "db-migrate-check", "handoff")},
	}

	claude := func(p library.ProjectID) ScopeRef { return ScopeRef{library.AgentClaudeCode, p} }
	codex := func(p library.ProjectID) ScopeRef { return ScopeRef{library.AgentCodex, p} }
	for _, n := range []string{"grill-me", "tdd", "handoff", "skill-creator", "find-skills"} {
		a.activate(a.skillByName(n).ID, claude(0), 0)
	}
	a.activateGroup(1, claude(0))
	a.activate(a.skillByName("tdd").ID, codex(0), 0)
	a.activateGroup(3, codex(0))
	a.activateGroup(4, claude(1))
	a.activate(a.skillByName("domain-modeling").ID, claude(1), 0)
	a.activate(a.skillByName("ask-dur").ID, claude(1), 0)
	a.activateGroup(2, claude(2))
	a.activate(a.skillByName("frontend-design").ID, codex(2), 0)
	a.activate(a.skillByName("db-migrate-check").ID, codex(3), 0)
	a.activate(a.skillByName("tdd").ID, codex(3), 0)
	a.activate(a.skillByName("firecrawl-search").ID, claude(3), 0)

	// 让几条投影偏离当前版本,展示 InspectActivations 的各种健康状态。
	a.find(a.skillByName("tdd").ID, codex(3)).Status = library.ActivationStale
	a.find(a.skillByName("frontend-design").ID, codex(2)).Status = library.ActivationModified
	a.find(a.skillByName("firecrawl-search").ID, claude(3)).Status = library.ActivationMissing
	a.find(a.skillByName("docx").ID, codex(0)).Status = library.ActivationStale
}

func skillMarkdown(name, desc string) string {
	return fmt.Sprintf(`---
name: %s
description: %s
---

# %s

%s

## When to use

- The user asks for something this skill covers.
- Another skill hands off to this one.

## Steps

1. Read the relevant files before acting.
2. Follow the checklist in order; do not skip verification.
3. Report what changed and what still needs a human decision.
`, name, desc, name, desc)
}

func (a *App) skillByName(name string) *SkillView {
	for _, s := range a.skills {
		if s.Name == name {
			return s
		}
	}
	panic("unknown seed skill " + name)
}

func (a *App) skill(id library.SkillID) *SkillView {
	for _, s := range a.skills {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (a *App) group(id library.GroupID) *GroupView {
	for _, g := range a.groups {
		if g.ID == id {
			return g
		}
	}
	return nil
}

func (a *App) find(id library.SkillID, scope ScopeRef) *ActivationView {
	for _, act := range a.activations {
		if act.SkillID == id && act.Scope == scope {
			return act
		}
	}
	return nil
}

func (a *App) directory(scope ScopeRef, name string) string {
	dir := ".claude/skills"
	if scope.Agent == library.AgentCodex {
		dir = ".agents/skills"
	}
	root := "~"
	for _, p := range a.projects {
		if p.ID == scope.ProjectID {
			root = p.Path
		}
	}
	return path.Join(root, dir, name)
}

// activate 记录一条激活来源:group 为 0 表示独立激活,否则追加分组来源。
func (a *App) activate(id library.SkillID, scope ScopeRef, group library.GroupID) {
	act := a.find(id, scope)
	if act == nil {
		act = &ActivationView{
			SkillID: id, Scope: scope, Directory: a.directory(scope, a.skill(id).Name),
			ActivatedAt: now, Status: library.ActivationCurrent, ViaGroups: []library.GroupID{},
		}
		a.activations = append(a.activations, act)
	}
	if group == 0 {
		act.Individual = true
	} else if !slices.Contains(act.ViaGroups, group) {
		act.ViaGroups = append(act.ViaGroups, group)
	}
}

func (a *App) activateGroup(id library.GroupID, scope ScopeRef) {
	for _, member := range a.group(id).Members {
		a.activate(member, scope, id)
	}
}

// prune 删除已经没有任何保留理由的投影。
func (a *App) prune() {
	a.activations = slices.DeleteFunc(a.activations, func(act *ActivationView) bool {
		return !act.Individual && len(act.ViaGroups) == 0
	})
}

func (a *App) checkScope(scope ScopeRef) error {
	if !slices.ContainsFunc(a.agents, func(ag library.Agent) bool { return ag.Code == scope.Agent }) {
		return &library.Error{Code: library.NotFound, Message: "未知 Agent " + string(scope.Agent)}
	}
	if scope.ProjectID != 0 && !slices.ContainsFunc(a.projects, func(p library.RegisteredProject) bool { return p.ID == scope.ProjectID }) {
		return &library.Error{Code: library.NotFound, Message: "项目未登记"}
	}
	return nil
}

// ---- 以下为绑定给前端的方法 ----

var firstSnapshot sync.Once

func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	firstSnapshot.Do(func() { log.Println("PROTOTYPE: 前端已通过绑定取到首个快照") })
	snap := Snapshot{Agents: a.agents, Projects: a.projects}
	collections := map[library.SourceID]*library.Collection{}
	for _, s := range a.skills {
		snap.Skills = append(snap.Skills, *s)
		c := collections[s.Source.ID]
		if c == nil {
			c = &library.Collection{Source: s.Source}
			collections[s.Source.ID] = c
		}
		c.Skills = append(c.Skills, s.SkillSummary)
	}
	for _, c := range collections {
		snap.Collections = append(snap.Collections, *c)
	}
	sort.Slice(snap.Collections, func(i, j int) bool { return snap.Collections[i].Source.ID < snap.Collections[j].Source.ID })
	for _, g := range a.groups {
		snap.Groups = append(snap.Groups, GroupView{g.Group, slices.Clone(g.Members)})
	}
	for _, act := range a.activations {
		copied := *act
		copied.ViaGroups = slices.Clone(act.ViaGroups)
		snap.Activations = append(snap.Activations, copied)
	}
	return snap
}

func (a *App) GetSkill(id library.SkillID) (SkillDetail, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.skill(id)
	if s == nil {
		return SkillDetail{}, &library.Error{Code: library.NotFound, Message: "技能不存在"}
	}
	versions := slices.Clone(a.versions[id])
	slices.Reverse(versions)
	return SkillDetail{
		Skill: library.Skill{
			ID: s.ID, Name: s.Name, Description: s.Description, Source: s.Source,
			InstalledAt: s.InstalledAt, UpdatedAt: s.UpdatedAt, Files: a.files[id],
		},
		Versions: versions,
	}, nil
}

func (a *App) Activate(id library.SkillID, scope ScopeRef) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkScope(scope); err != nil {
		return err
	}
	if a.skill(id) == nil {
		return &library.Error{Code: library.NotFound, Message: "技能不存在"}
	}
	a.activate(id, scope, 0)
	a.find(id, scope).Status = library.ActivationCurrent
	return nil
}

func (a *App) Deactivate(id library.SkillID, scope ScopeRef) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	act := a.find(id, scope)
	if act == nil {
		return &library.Error{Code: library.NotFound, Message: "该作用域下没有这条激活"}
	}
	if !act.Individual {
		var names []string
		for _, g := range act.ViaGroups {
			names = append(names, "「"+a.group(g).Name+"」")
		}
		return &library.Error{Code: library.FailedPrecondition, Message: "该技能仅经由分组" + strings.Join(names, "、") + "激活,不能单独停用;请停用分组或把它移出分组"}
	}
	act.Individual = false
	a.prune()
	return nil
}

func (a *App) ActivateGroup(id library.GroupID, scope ScopeRef) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkScope(scope); err != nil {
		return err
	}
	if a.group(id) == nil {
		return &library.Error{Code: library.NotFound, Message: "分组不存在"}
	}
	a.activateGroup(id, scope)
	return nil
}

func (a *App) DeactivateGroup(id library.GroupID, scope ScopeRef) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, act := range a.activations {
		if act.Scope == scope {
			act.ViaGroups = slices.DeleteFunc(act.ViaGroups, func(g library.GroupID) bool { return g == id })
		}
	}
	a.prune()
	return nil
}

func (a *App) CreateGroup(name string) (library.Group, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return library.Group{}, &library.Error{Code: library.InvalidArgument, Message: "分组名不能为空"}
	}
	// 与真实 Library 相同:重名时追加 #N 而不是失败。
	unique := name
	for n := 2; slices.ContainsFunc(a.groups, func(g *GroupView) bool { return g.Name == unique }); n++ {
		unique = fmt.Sprintf("%s#%d", name, n)
	}
	var next library.GroupID
	for _, g := range a.groups {
		next = max(next, g.ID)
	}
	g := &GroupView{library.Group{ID: next + 1, Name: unique, CreatedAt: time.Now()}, []library.SkillID{}}
	a.groups = append(a.groups, g)
	return g.Group, nil
}

func (a *App) AddSkillToGroup(groupID library.GroupID, skillID library.SkillID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	g := a.group(groupID)
	if g == nil {
		return &library.Error{Code: library.NotFound, Message: "分组不存在"}
	}
	if slices.Contains(g.Members, skillID) {
		return &library.Error{Code: library.AlreadyExists, Message: "技能已在该分组中"}
	}
	g.Members = append(g.Members, skillID)
	return nil
}

func (a *App) RemoveSkillFromGroup(groupID library.GroupID, skillID library.SkillID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	g := a.group(groupID)
	if g == nil {
		return &library.Error{Code: library.NotFound, Message: "分组不存在"}
	}
	g.Members = slices.DeleteFunc(g.Members, func(id library.SkillID) bool { return id == skillID })
	return nil
}

// Update 模拟一次有变化的更新:生成新版本,已激活的投影变为 stale(真实 Library 不会自动刷新投影)。
func (a *App) Update(id library.SkillID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.skill(id)
	if s == nil {
		return &library.Error{Code: library.NotFound, Message: "技能不存在"}
	}
	if !s.UpdateAvailable {
		return nil
	}
	s.UpdateAvailable = false
	s.UpdatedAt = time.Now()
	s.Versions++
	var last library.VersionID
	for _, vs := range a.versions {
		for _, v := range vs {
			last = max(last, v.ID)
		}
	}
	a.versions[id] = append(a.versions[id], library.SkillVersion{ID: last + 1, SkillID: id, Description: s.Description, CreatedAt: s.UpdatedAt, GitTreeSHA: "a1b2c3d"})
	for _, act := range a.activations {
		if act.SkillID == id && act.Status == library.ActivationCurrent {
			act.Status = library.ActivationStale
		}
	}
	return nil
}

// Refresh 把某条投影重写为当前版本(真实门面里是对同一 Scope 再次 Activate)。
func (a *App) Refresh(id library.SkillID, scope ScopeRef) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	act := a.find(id, scope)
	if act == nil {
		return &library.Error{Code: library.NotFound, Message: "该作用域下没有这条激活"}
	}
	act.Status = library.ActivationCurrent
	return nil
}
