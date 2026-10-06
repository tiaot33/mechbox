package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// ListAgents 列出 Library 持有的全部 Agent(按代码排序)。只读;结果含稳定代码与展示名称,
// 不含 Agent 目录。Agent 由建库种子写入,技能库没有增删改 Agent 的方法。
func (l *Library) ListAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		agents = append(agents, Agent{Code: AgentCode(row.Code), Name: row.Name})
	}
	return agents, nil
}

// RegisterProject 注册一个项目目录,使它与各 Agent 组成可安装的 ProjectScope。
func (l *Library) RegisterProject(ctx context.Context, path string) (project RegisteredProject, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if path == "" {
		return RegisteredProject{}, argumentError("register project", "path", "path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("resolve project path: %w", err)
	}
	project.Path, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(project.Path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("read project path: %w", err)
	}
	if !info.IsDir() {
		return RegisteredProject{}, argumentError("register project", "path", "path is not a directory")
	}
	project.Path = filepath.Clean(project.Path)
	row, created, err := l.store.RegisterProject(ctx, project.Path)
	if err != nil {
		return RegisteredProject{}, err
	}
	if !created {
		return RegisteredProject{}, existsError("register project", "path already registered", ErrorDetails{ProjectID: ProjectID(row.ID), Path: project.Path})
	}
	project.ID = ProjectID(row.ID)
	return project, nil
}

// ListRegisteredProjects 按路径列出全部已登记项目。
func (l *Library) ListRegisteredProjects(ctx context.Context) (projects []RegisteredProject, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		projects = append(projects, RegisteredProject{ID: ProjectID(row.ID), Path: row.Path})
	}
	return projects, nil
}

// UnregisterProject 注销项目并清掉其各 Agent 项目 Scope 下的全部安装记录与组安装;
// opts 决定磁盘上技能文件夹的去留。
func (l *Library) UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateFolderPolicy("unregister project", opts.Folders); err != nil {
		return err
	}
	if _, err := l.store.GetProject(ctx, int64(id)); err != nil {
		return err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	if opts.Folders != Keep {
		skillScopes, err := l.ListSkillScopes(ctx)
		if err != nil {
			return err
		}
		for _, skillScope := range skillScopes {
			if skillScope.Scope.Kind != ScopeProject || skillScope.Scope.ProjectID != id {
				continue
			}
			skill, err := l.GetSkill(ctx, skillScope.SkillID)
			if err != nil {
				return err
			}
			change.Remove(skillScope.Directory, skill.Name)
		}
	}
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		return tx.UnregisterProject(ctx, int64(id))
	})
}

// Resolve 按 agents 表算出 Scope 对应的 Agent 目录:全局相对 Open 时的用户
// 主目录,项目相对该 Registered project 根。只读:不写 Library、不创建目录、不产生安装。
// 未知 Agent 代码或未登记项目返回 NotFound。可选目标没有单独的列举方法:调用方用
// ListAgents ×(全局 | ListRegisteredProjects)自行组合。
func (l *Library) Resolve(ctx context.Context, scope Scope) (directory string, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := scopeOf("resolve scope", scope)
	if err != nil {
		return "", err
	}
	_, directory, err = l.scopeDirectory(ctx, row)
	return directory, err
}

func (l *Library) scopeDirectory(ctx context.Context, row scopeRow) (root, directory string, err error) {
	agent, err := l.store.GetAgent(ctx, string(row.agent))
	if err != nil {
		return "", "", err
	}
	if row.projectID == nil {
		return l.home, filepath.Join(l.home, filepath.FromSlash(agent.GlobalPath)), nil
	}
	project, err := l.store.GetProject(ctx, int64(*row.projectID))
	if err != nil {
		return "", "", err
	}
	return project.Path, filepath.Join(project.Path, filepath.FromSlash(agent.ProjectPath)), nil
}

func (l *Library) applyChange(ctx context.Context, change *agentdir.Change, record func(*store.Tx) error) error {
	if err := l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := record(tx); err != nil {
			return err
		}
		return change.Commit(ctx)
	}); err != nil {
		return err
	}
	change.Done()
	return nil
}
