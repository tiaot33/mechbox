package main

// seed 在一个完全隔离的临时环境里建库并灌入真实数据。
//
// 重要:library.Open 会在打开时捕获 os.UserHomeDir(),全局 Scope 的投影据此落盘。
// 原型必须在 Open 之前把 HOME 改到临时目录,否则会写进用户真实的 agent 目录。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library"
)

// seededSkill 是一条要灌进库的技能素材。
type seededSkill struct {
	name        string
	description string
	body        string
}

var seedSkills = []seededSkill{
	{"code-review", "审查自某个基点以来的改动,按仓库规范与原始 issue 双轴给出结论", "按标准轴与规格轴分别评审。"},
	{"diagnosing-bugs", "疑难 bug 与性能回退的诊断循环", "先复现,再二分,最后定位。"},
	{"domain-modeling", "建立并打磨项目的领域模型,维护 CONTEXT.md 与 ADR", "术语先于实现。"},
	{"tdd", "测试先行的开发循环:红、绿、重构", "先写失败的测试。"},
	{"resolving-merge-conflicts", "处理进行中的 git merge / rebase 冲突", "逐个 hunk 判断意图。"},
	{"writing-for-agents", "为 agent 编写文档,含 skill 与 AGENTS.md", "写给会执行的读者。"},
	{"show-me", "用图示与代码骨架把当前话题讲清楚", "一图胜过三段话。"},
	{"prototype", "造一次性原型来回答一个设计问题", "回答完就扔。"},
}

// seedEnv 持有原型的隔离环境,退出时整体删除。
type seedEnv struct {
	root     string
	lib      library.Library
	projects []library.RegisteredProject
	agents   []library.Agent
}

func (e *seedEnv) cleanup() {
	if e.lib != nil {
		_ = e.lib.Close()
	}
	if e.root != "" {
		_ = os.RemoveAll(e.root)
	}
}

// setupSeed 建临时 HOME、打开内存库、装入技能、注册项目、做出各种投影状态。
func setupSeed(ctx context.Context) (*seedEnv, error) {
	root, err := os.MkdirTemp("", "skills-PROTOTYPE-wipe-me-*")
	if err != nil {
		return nil, err
	}
	env := &seedEnv{root: root}

	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		env.cleanup()
		return nil, err
	}
	// 必须在 Open 之前设置:Open 捕获 home 后不再读环境变量。
	os.Setenv("HOME", home)

	lib, err := library.Open(":memory:")
	if err != nil {
		env.cleanup()
		return nil, fmt.Errorf("open library: %w", err)
	}
	env.lib = lib

	if env.agents, err = lib.ListAgents(ctx); err != nil {
		env.cleanup()
		return nil, err
	}

	// 装技能:每个技能一个本地目录,因此每个技能自成一个 Source。
	installed := make([]library.Skill, 0, len(seedSkills))
	for _, s := range seedSkills {
		dir := filepath.Join(root, "sources", s.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			env.cleanup()
			return nil, err
		}
		doc := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", s.name, s.description, s.body)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o644); err != nil {
			env.cleanup()
			return nil, err
		}
		skill, err := lib.Install(ctx, library.LocalSource{Path: dir})
		if err != nil {
			env.cleanup()
			return nil, fmt.Errorf("install %s: %w", s.name, err)
		}
		installed = append(installed, skill)
	}

	// 注册两个项目,凑出足够宽的 Scope 矩阵。
	for _, name := range []string{"skills-app", "payments-api"} {
		dir := filepath.Join(root, "projects", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			env.cleanup()
			return nil, err
		}
		project, err := lib.RegisterProject(ctx, dir)
		if err != nil {
			env.cleanup()
			return nil, fmt.Errorf("register %s: %w", name, err)
		}
		env.projects = append(env.projects, project)
	}

	// 分组,并整组激活一次,让 Activation.ViaGroups 非空。
	debugging, err := lib.CreateGroup(ctx, "前端调试")
	if err != nil {
		env.cleanup()
		return nil, err
	}
	writing, err := lib.CreateGroup(ctx, "写作")
	if err != nil {
		env.cleanup()
		return nil, err
	}
	byName := map[string]library.SkillID{}
	for _, s := range installed {
		byName[s.Name] = s.ID
	}
	for _, n := range []string{"diagnosing-bugs", "code-review", "tdd"} {
		if err := lib.AddSkillToGroup(ctx, debugging.ID, byName[n]); err != nil {
			env.cleanup()
			return nil, err
		}
	}
	for _, n := range []string{"writing-for-agents", "show-me"} {
		if err := lib.AddSkillToGroup(ctx, writing.ID, byName[n]); err != nil {
			env.cleanup()
			return nil, err
		}
	}

	claudeGlobal := library.GlobalScope{Agent: library.AgentClaudeCode}
	codexGlobal := library.GlobalScope{Agent: library.AgentCodex}
	appClaude := library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: env.projects[0].ID}
	apiCodex := library.ProjectScope{Agent: library.AgentCodex, ProjectID: env.projects[1].ID}

	if err := lib.ActivateGroup(ctx, debugging.ID, claudeGlobal, library.ActivateOptions{}); err != nil {
		env.cleanup()
		return nil, fmt.Errorf("activate group: %w", err)
	}
	for _, a := range []struct {
		name  string
		scope library.Scope
	}{
		{"domain-modeling", claudeGlobal},
		{"prototype", claudeGlobal},
		{"writing-for-agents", codexGlobal},
		{"show-me", codexGlobal},
		{"tdd", appClaude},
		{"resolving-merge-conflicts", appClaude},
		{"code-review", apiCodex},
	} {
		if err := lib.Activate(ctx, byName[a.name], a.scope, library.ActivateOptions{}); err != nil {
			env.cleanup()
			return nil, fmt.Errorf("activate %s: %w", a.name, err)
		}
	}

	// 扰动磁盘,凑齐 InspectActivations 的几种状态。
	perturb(ctx, lib, byName, claudeGlobal, codexGlobal, appClaude)
	return env, nil
}

// perturb 故意把磁盘改到与库不一致,好让 InspectActivations 返回非 current 的状态。
// 失败一概忽略:原型只要数据够看,不为扰动本身报错。
func perturb(ctx context.Context, lib library.Library, byName map[string]library.SkillID, claudeGlobal, codexGlobal, appClaude library.Scope) {
	// missing:删掉整个投影文件夹。
	if dir, err := lib.Resolve(ctx, claudeGlobal); err == nil {
		_ = os.RemoveAll(filepath.Join(dir, "prototype"))
	}
	// modified:改掉投影里的 SKILL.md。
	if dir, err := lib.Resolve(ctx, codexGlobal); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "show-me", "SKILL.md"), []byte("本地手改过的内容\n"), 0o644)
	}
	// directory_missing:删掉整个 Scope 目录。
	if dir, err := lib.Resolve(ctx, appClaude); err == nil {
		_ = os.RemoveAll(dir)
	}
}
