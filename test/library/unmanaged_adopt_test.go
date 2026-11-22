package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 列出 Agent 目录里的技能时,和库里某一版完全一致的文件夹自动记为安装,不用入库

func TestCopyOfALibrarySkillIsAdoptedWithoutFetching(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	f.write(t, f.claude, "alpha", "v1")
	folder := filepath.Join(scopeDirectory(t, f.lib, f.claude), "alpha")
	before := snapshotTree(t, folder)

	// 第一次打开 app 的整批入库确认里没有它:它已经是 alpha 装在 Claude Code 全局,磁盘上什么都没改。
	if plan := planFetchUnmanaged(t, f.lib, f.claude); len(plan) != 0 {
		t.Fatalf("plan = %#v, want nothing left to fetch", plan)
	}
	requireUnmanagedSkills(t, f.lib, nil)
	if inspection := requireScopeStatus(t, f.lib, alpha.ID, f.claude, library.SkillScopeCurrent); inspection.VersionID != alpha.VersionID {
		t.Fatalf("inspection = %#v, want alpha's current version", inspection)
	}
	if after := snapshotTree(t, folder); !reflect.DeepEqual(after, before) {
		t.Fatalf("adopting changed the folder: before %v, after %v", before, after)
	}

	// 之后用户在这里改了一行:巡检为用户改过,更新计划里这一处也是用户改过。
	f.write(t, f.claude, "alpha", "my edit")
	requireScopeStatus(t, f.lib, alpha.ID, f.claude, library.SkillScopeModified)
	f.publish(t, "https://author.example", "alpha", "v2")
	plan, err := f.lib.PlanUpdate(ctx, alpha.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Installs) != 1 || plan.Installs[0].Scope != f.claude || plan.Installs[0].Status != library.SkillScopeModified {
		t.Fatalf("plan installs = %#v, want the edited place", plan.Installs)
	}

	// 移除 alpha 时这一处和其他安装一样一并删除。
	if err := f.lib.RemoveSkill(ctx, alpha.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(folder); !os.IsNotExist(err) {
		t.Fatalf("folder after removing alpha: %v, want it removed", err)
	}
}

func TestCopyOfAnOlderVersionIsAdoptedAndUpdatedOnRequest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	f.publish(t, "https://author.example", "alpha", "v2")
	if _, err := updateSkill(t, f.lib, ctx, alpha.ID); err != nil {
		t.Fatal(err)
	}
	f.write(t, f.codex, "alpha", "v1")

	// 和 alpha 的旧版一致:自动记为安装,巡检为落后;作者再发新版时,更新计划里这一处在要换的地方里,由用户勾选。
	requireUnmanagedSkills(t, f.lib, nil)
	if inspection := requireScopeStatus(t, f.lib, alpha.ID, f.codex, library.SkillScopeStale); inspection.VersionID != alpha.VersionID {
		t.Fatalf("inspection = %#v, want alpha's first version", inspection)
	}
	f.publish(t, "https://author.example", "alpha", "v3")
	plan, err := f.lib.PlanUpdate(ctx, alpha.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Installs) != 1 || plan.Installs[0].Scope != f.codex || plan.Installs[0].Status != library.SkillScopeStale {
		t.Fatalf("plan installs = %#v, want the stale codex copy", plan.Installs)
	}
	result, err := f.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{alpha.ID}, Installs: []library.ReinstallRequest{{SkillID: alpha.ID, Scope: f.codex}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installs) != 1 || result.Installs[0].Outcome != library.Reinstalled {
		t.Fatalf("installs = %#v, want codex updated", result.Installs)
	}
	requireScopeStatus(t, f.lib, alpha.ID, f.codex, library.SkillScopeCurrent)
}

func TestLinkBetweenAgentDirectoriesIsAdoptedAsItIs(t *testing.T) {
	t.Parallel()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	// npx skills 的布局:真实文件夹在 ~/.agents/skills(Codex 的全局目录),Claude Code 的目录里是指过去的链接。
	f.write(t, f.codex, "alpha", "v1")
	claudeDirectory := scopeDirectory(t, f.lib, f.claude)
	if err := os.MkdirAll(claudeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(claudeDirectory, "alpha")
	target := filepath.Join("..", "..", ".agents", "skills", "alpha")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	requireUnmanagedSkills(t, f.lib, nil)
	requireScopeStatus(t, f.lib, alpha.ID, f.claude, library.SkillScopeCurrent)
	requireScopeStatus(t, f.lib, alpha.ID, f.codex, library.SkillScopeCurrent)
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("link = %q, %v, want it left pointing at %q", got, err, target)
	}
}

// 提交在项目仓库里的、记在项目 skills-lock.json 里的、指向 Agent 目录以外的,内容一致也不自动纳入,
// 照旧显示在库里那个技能下面,用户入库后才记为安装。
func TestTeamAndOutsideCopiesAreNotAdopted(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	cases := []struct {
		name    string
		prepare func(t *testing.T, root string, scope library.Scope)
		check   func(skill library.UnmanagedSkill) bool
	}{
		{"committed", func(t *testing.T, root string, scope library.Scope) {
			runGit(t, root, "init", "-b", "main")
			f.write(t, scope, "alpha", "v1")
			commitGit(t, root, "team skill")
		}, func(skill library.UnmanagedSkill) bool { return skill.InRepository }},
		{"in the lock file", func(t *testing.T, root string, scope library.Scope) {
			f.write(t, scope, "alpha", "v1")
			writeSourceRecords(t, filepath.Join(root, "skills-lock.json"), 1, map[string]map[string]string{
				"alpha": {"source": "https://author.example", "sourceType": "well-known", "computedHash": "abc"},
			})
		}, func(skill library.UnmanagedSkill) bool { return skill.InProjectLock }},
		{"linked outside", func(t *testing.T, root string, scope library.Scope) {
			work := filepath.Join(t.TempDir(), "alpha")
			writeFile(t, filepath.Join(work, "SKILL.md"), []byte(skillDocument("alpha", "v1")), 0o644)
			if err := os.MkdirAll(scopeDirectory(t, f.lib, scope), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(work, filepath.Join(scopeDirectory(t, f.lib, scope), "alpha")); err != nil {
				t.Fatal(err)
			}
		}, func(skill library.UnmanagedSkill) bool { return skill.LinkOutside }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			project, err := f.lib.RegisterProject(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
			c.prepare(t, root, scope)

			if here := unmanagedIn(t, f.lib, scope, "alpha"); here.SkillID != alpha.ID || !c.check(here) {
				t.Fatalf("unmanaged = %#v, want it shown under alpha", here)
			}
			planned := plannedNamed(t, planFetchUnmanaged(t, f.lib, scope), "alpha")[0]
			if planned.SkillID != alpha.ID || planned.Selected {
				t.Fatalf("planned = %#v, want alpha, not selected", planned)
			}
			if inspections, err := f.lib.InspectSkillScopes(ctx, library.SkillScopeFilter{Scope: &scope}); err != nil || len(inspections) != 0 {
				t.Fatalf("InspectSkillScopes = %#v, %v, want nothing recorded", inspections, err)
			}
			if fetched := fetchUnmanaged(t, f.lib, scope, "alpha"); !fetched.Matched || fetched.Skill.ID != alpha.ID {
				t.Fatalf("fetched = %#v, want alpha matched", fetched)
			}
			requireScopeStatus(t, f.lib, alpha.ID, scope, library.SkillScopeCurrent)
		})
	}
}

func TestOnlyOneCopyIsAdoptedPerScope(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	directory := scopeDirectory(t, f.lib, f.claude)
	// 同一个作用域里两份一样的:记文件夹名就是技能名的那一份,另一份照旧是未纳管技能。
	writeFile(t, filepath.Join(directory, "a-copy", "SKILL.md"), []byte(skillDocument("alpha", "v1")), 0o644)
	f.write(t, f.claude, "alpha", "v1")
	// 和 Claude Code 共用全局目录的自定义 Agent 本来就算已装,不另记一处。
	if _, err := f.lib.AddAgent(ctx, library.CustomAgent{Name: "Shared", GlobalDirectory: directory, ProjectDirectory: ".shared/skills"}); err != nil {
		t.Fatal(err)
	}

	unmanaged := listUnmanagedSkills(t, f.lib)
	for _, skill := range unmanaged {
		if skill.Name != "a-copy" || skill.SkillID != 0 {
			t.Fatalf("unmanaged = %#v, want only a-copy left, not taken as alpha", unmanaged)
		}
	}
	inspection := requireScopeStatus(t, f.lib, alpha.ID, f.claude, library.SkillScopeCurrent)
	if inspection.Folder != filepath.Join(directory, "alpha") {
		t.Fatalf("inspection = %#v, want the folder named alpha", inspection)
	}
	// 只记了一处:从 Claude Code 卸载后,共用目录的 Agent 也不再算已装。
	if err := f.lib.Uninstall(ctx, alpha.ID, f.claude); err != nil {
		t.Fatal(err)
	}
	if scopes, err := f.lib.ListSkillScopes(ctx, library.SkillScopeFilter{SkillID: alpha.ID}); err != nil || len(scopes) != 0 {
		t.Fatalf("skill scopes after uninstall = %#v, %v, want none left", scopes, err)
	}
}
