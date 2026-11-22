package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// addFromPool 把候选池里的几个 Agent 添加进来,失败即终止测试。
func addFromPool(t *testing.T, lib *library.Library, codes ...library.AgentCode) []library.Agent {
	t.Helper()
	added, err := lib.AddAgentsFromPool(t.Context(), codes)
	if err != nil {
		t.Fatal(err)
	}
	return added
}

func agentCodes(agents []library.Agent) []library.AgentCode {
	codes := make([]library.AgentCode, len(agents))
	for i, agent := range agents {
		codes[i] = agent.Code
	}
	return codes
}

func poolAgent(t *testing.T, lib *library.Library, code library.AgentCode) library.Agent {
	t.Helper()
	pool, err := lib.ListAgentPool(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(pool, func(agent library.Agent) bool { return agent.Code == code })
	if index < 0 {
		t.Fatalf("ListAgentPool() = %v; want %s in the pool", agentCodes(pool), code)
	}
	return pool[index]
}

// 候选池里是应用收录、还没添加的 Agent;添加后它组成作用域,和已添加的 Agent 读同一个目录时,那里装着的技能它立刻也算已装。
// 已经添加了的照旧;有不认识的代码时返回 NotFound,一个都不添加。
func TestAddAgentsFromPoolCountsWhatIsAlreadyInTheirDirectories(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "example")
	if err := lib.Install(ctx, skill.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	cursor := poolAgent(t, lib, "cursor")
	wantCursor := library.Agent{
		Code: "cursor", Name: "Cursor", DetectDirectories: []string{filepath.Join(home, ".cursor")},
		GlobalDirectories:  []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".cursor", "skills")},
		ProjectDirectories: []string{".agents/skills"},
	}
	if !reflect.DeepEqual(cursor, wantCursor) {
		t.Fatalf("Cursor in the pool = %#v, want %#v", cursor, wantCursor)
	}
	_, err := lib.AddAgentsFromPool(ctx, []library.AgentCode{"cline", "no-such-agent"})
	requireLibraryError(t, err, library.NotFound)
	if got := agentCodes(listAgents(t, lib)); !reflect.DeepEqual(got, []library.AgentCode{library.AgentClaudeCode, library.AgentCodex}) {
		t.Fatalf("ListAgents() after a failed add = %v; want nothing added", got)
	}

	added := addFromPool(t, lib, "cursor", "cline", library.AgentCodex, "cline")
	if got := agentCodes(added); !reflect.DeepEqual(got, []library.AgentCode{"cline", library.AgentCodex, "cursor"}) {
		t.Fatalf("AddAgentsFromPool() = %v; want cline, codex and cursor", got)
	}
	if got := agentCodes(listAgents(t, lib)); !reflect.DeepEqual(got, []library.AgentCode{library.AgentClaudeCode, "cline", library.AgentCodex, "cursor"}) {
		t.Fatalf("ListAgents() = %v; want cline and cursor added", got)
	}
	if pool, err := lib.ListAgentPool(ctx); err != nil || slices.ContainsFunc(pool, func(agent library.Agent) bool { return agent.Code == "cursor" || agent.Code == "cline" }) {
		t.Fatalf("ListAgentPool() = %v, %v; want cursor and cline gone from the pool", agentCodes(pool), err)
	}
	directory := filepath.Join(home, ".agents", "skills")
	folder := filepath.Join(directory, "example")
	got, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{SkillID: skill.ID})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.SkillScope{
		{SkillID: skill.ID, Scope: library.GlobalScope("cline"), Directory: directory, Folder: folder, Direct: true, SharedWith: []library.AgentCode{library.AgentCodex, "cursor"}, SharedOnly: true},
		{SkillID: skill.ID, Scope: library.GlobalScope(library.AgentCodex), Directory: directory, Folder: folder, Direct: true, SharedWith: []library.AgentCode{"cline", "cursor"}},
		{SkillID: skill.ID, Scope: library.GlobalScope("cursor"), Directory: directory, Folder: folder, Direct: true, SharedWith: []library.AgentCode{"cline", library.AgentCodex}, SharedOnly: true},
	}
	for i := range got {
		got[i].CreatedAt = want[i].CreatedAt
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListSkillScopes() = %#v, want %#v", got, want)
	}
}

// 按检测结果设定已添加的 Agent:检测到的都添加;Claude Code、Codex 没检测到、又没有安装记录的移回候选池;
// 一个都没检测到时保留新数据库里的这两个。
func TestApplyDetectedAgentsAddsTheAgentsOnThisMachine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		detect  []string
		install library.AgentCode
		want    []library.AgentCode
	}{
		{name: "nothing detected", want: []library.AgentCode{library.AgentClaudeCode, library.AgentCodex}},
		{name: "cursor and claude code", detect: []string{".cursor", ".claude"}, want: []library.AgentCode{library.AgentClaudeCode, "cursor"}},
		{name: "codex has installs", detect: []string{".cursor"}, install: library.AgentCodex, want: []library.AgentCode{library.AgentCodex, "cursor"}},
		{name: "any detect directory", detect: []string{".kilocode", ".config/zed"}, want: []library.AgentCode{"kilo", "zed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			setTestHome(t, home)
			lib := openTestLibrary(t)
			for _, directory := range tc.detect {
				if err := os.MkdirAll(filepath.Join(home, directory), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.install != "" {
				skill := fetchNamedSkill(t, lib, "example")
				if err := lib.Install(ctx, skill.ID, library.ProjectScope(tc.install, registerProject(t, lib).ID), library.InstallOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			agents, err := lib.ApplyDetectedAgents(ctx)
			if err != nil || !reflect.DeepEqual(agentCodes(agents), tc.want) {
				t.Fatalf("ApplyDetectedAgents() = %v, %v; want %v", agentCodes(agents), err, tc.want)
			}
			if got := agentCodes(listAgents(t, lib)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ListAgents() = %v; want %v", got, tc.want)
			}
			for _, agent := range agents {
				if len(tc.detect) > 0 && !agent.Detected && agent.Code != tc.install {
					t.Fatalf("added %s that was not detected", agent.Code)
				}
			}
		})
	}
}

// Kilo Code 看 ~/.kilo 或 ~/.kilocode,任一存在就算检测到;候选池里的 Agent 同样在列出时现查。
func TestAgentIsDetectedByAnyOfItsDetectDirectories(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	if poolAgent(t, lib, "kilo").Detected {
		t.Fatal("Kilo Code detected before ~/.kilocode exists")
	}
	if err := os.Mkdir(filepath.Join(home, ".kilocode"), 0o755); err != nil {
		t.Fatal(err)
	}
	kilo := poolAgent(t, lib, "kilo")
	if want := []string{filepath.Join(home, ".kilo"), filepath.Join(home, ".kilocode")}; !kilo.Detected || !reflect.DeepEqual(kilo.DetectDirectories, want) {
		t.Fatalf("Kilo Code = %#v; want detected through ~/.kilocode", kilo)
	}
	if added := addFromPool(t, lib, "kilo"); !added[0].Detected {
		t.Fatalf("AddAgentsFromPool() = %#v; want Kilo Code detected", added)
	}
}

func registerProject(t *testing.T, lib *library.Library) library.RegisteredProject {
	t.Helper()
	project, err := lib.RegisterProject(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return project
}
