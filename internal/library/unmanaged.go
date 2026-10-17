package library

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

// UnmanagedSkill 是作用域的技能目录里一个不是库装的技能文件夹:这个作用域里没有安装记录用它。它可能是命令行工具
// (如 npx skills)装的、手动拷进去的、用户自己写的,或者提交在项目仓库里的。库只列出它,不改动、不删除。
type UnmanagedSkill struct {
	Scope Scope `json:"scope"`
	// Name 是文件夹名。
	Name string `json:"name"`
	// Description 取自 SKILL.md 的 frontmatter;frontmatter 读不出时为空。
	Description string `json:"description,omitempty"`
	// Folder 是技能文件夹在作用域目录里的路径;文件夹是符号链接时就是链接本身,「在 Finder 中显示」用它。
	Folder string `json:"folder"`
	// LinkTarget 在文件夹是符号链接时给出它最终指向的目录(已解析各级符号链接),否则为空。
	// 例如 npx skills 把真实文件夹放在 ~/.agents/skills/<name>,再从其他 Agent 的目录用符号链接指过去。
	LinkTarget string `json:"link_target,omitempty"`
	// InRepository 表示文件夹已提交在项目的 git 仓库里(在 HEAD 里),界面标明「在项目仓库里」。只出现在项目作用域;
	// 项目不是 git 仓库、还没有提交或本机没有 git 时为 false。
	InRepository bool `json:"in_repository,omitzero"`
}

// ListUnmanagedSkills 列出各作用域(每个 Agent 的全局,以及每个 Agent 在每个已登记项目里)的技能目录里
// 不是库装的技能文件夹,和 ListSkillScopes 列出的库装的技能放在一起,就是 Agent 在这个作用域能看到的技能。
// 技能文件夹是技能目录下直接的子文件夹,或指向文件夹的符号链接,根上有 SKILL.md;以点开头的(如 Codex 的 .system)不算。
// 这个作用域里有安装记录用它的(含共用目录的 Agent 装的,共用按 Agent 的目录设置判断,不跟随符号链接)是库装的,不列出;
// 库装出去后被改过的也不在这里,巡检报为 modified。
// 插件里的技能、企业托管目录、Codex 的 ADMIN / SYSTEM 技能和没有登记的项目都不在范围内。
// 只读:不改库、不改磁盘。Agent 目录不存在、项目不在这台机器上时那个作用域没有结果;几个 Agent 共用一个目录时,
// 每个 Agent 各列一条。结果按 Agent、作用域(全局在前,项目按 ID)、Name 排序。
func (l *Library) ListUnmanagedSkills(ctx context.Context) (skills []UnmanagedSkill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	agents, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := l.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := l.listSkillScopes(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	type folder struct {
		scope Scope
		name  string
	}
	claimed := map[folder]bool{}
	for _, skillScope := range installed {
		claimed[folder{skillScope.Scope, filepath.Base(skillScope.Folder)}] = true
	}
	type repository struct {
		projectID ProjectID
		relative  string
	}
	committed := map[repository]map[string]bool{}
	skills = []UnmanagedSkill{}
	for _, agent := range agents {
		rows := []scopeRow{{agent: AgentCode(agent.Code)}}
		for _, project := range projects {
			rows = append(rows, scopeRow{agent: AgentCode(agent.Code), projectID: new(ProjectID(project.ID))})
		}
		for _, row := range rows {
			target, err := l.resolveRow(ctx, row)
			if err != nil {
				return nil, err
			}
			if missing, err := target.rootMissing("list unmanaged skills"); err != nil {
				return nil, err
			} else if missing != nil {
				continue
			}
			folders, err := agentdir.List(target.directory)
			if err != nil {
				return nil, err
			}
			for _, found := range folders {
				if claimed[folder{row.scope(), found.Name}] {
					continue
				}
				skill := UnmanagedSkill{Scope: row.scope(), Name: found.Name, Folder: found.Path, LinkTarget: found.Target}
				if candidate, err := discovery.ParseSkillMD(found.SkillMD, found.Name); err == nil {
					skill.Description = candidate.Description
				}
				if row.projectID != nil {
					key := repository{projectID: *row.projectID, relative: target.relative}
					if _, ok := committed[key]; !ok {
						if committed[key], err = agentdir.Committed(ctx, target.root, target.relative); err != nil {
							return nil, err
						}
					}
					skill.InRepository = committed[key][found.Name]
				}
				skills = append(skills, skill)
			}
		}
	}
	slices.SortFunc(skills, func(a, b UnmanagedSkill) int {
		return cmp.Or(cmp.Compare(a.Scope.Agent, b.Scope.Agent), cmp.Compare(a.Scope.ProjectID, b.Scope.ProjectID), cmp.Compare(a.Name, b.Name))
	})
	return skills, nil
}
