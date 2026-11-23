package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 编辑 Cursor 全局,去掉 ~/.cursor/skills:迁移时那里的安装搬到 ~/.agents/skills,原本在 ~/.agents/skills 的不受影响;
// 不迁移时那几处不再算已装,文件夹留在原处。
func TestEditAgentDroppingADirectoryMigratesOrDropsItsInstalls(t *testing.T) {
	t.Parallel()
	for _, migrate := range []bool{true, false} {
		t.Run(map[bool]string{true: "migrate", false: "keep in place"}[migrate], func(t *testing.T) {
			ctx := t.Context()
			lib, agents, cursor := cursorHome(t)
			global := library.GlobalScope("cursor")
			writeUnmanagedSkill(t, cursor, "mine", "mine")
			fetched, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: global, Directory: cursor, Name: "mine"})
			if err != nil {
				t.Fatal(err)
			}
			mine := fetched.Skill
			kept := fetchNamedSkill(t, lib, "kept")
			if err := lib.Install(ctx, kept.ID, global, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			result, err := lib.EditAgent(ctx, "cursor", library.AgentInput{
				Name: "Cursor", GlobalDirectories: []string{agents}, ProjectDirectories: []string{".agents/skills"}, DetectDirectories: []string{"~/.cursor"},
			}, library.EditAgentOptions{Migrate: migrate})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Agent.GlobalDirectories, []string{agents}) {
				t.Fatalf("EditAgent() agent = %#v; want only %s", result.Agent, agents)
			}
			if scopes := skillScopesOf(t, lib, kept.ID); scopes[global].Directory != agents || scopes[global].SharedOnly {
				t.Fatalf("ListSkillScopes(kept) = %#v; want it untouched in %s", scopes, agents)
			}
			requireInstalledFolder(t, agents, "kept", "# kept\n")
			scopes := skillScopesOf(t, lib, mine.ID)
			if !migrate {
				if len(result.Migrations) != 0 || len(scopes) != 0 {
					t.Fatalf("EditAgent() = %#v, mine installed at %#v; want no migrations and mine no longer installed", result, scopes)
				}
				requireSkillFile(t, cursor, "mine", "---\nname: mine\ndescription: mine\n---\n# mine\n")
				return
			}
			want := []library.AgentMigration{{SkillID: mine.ID, Scope: global, Outcome: library.MigrationMoved, From: filepath.Join(cursor, "mine"), To: filepath.Join(agents, "mine")}}
			if !reflect.DeepEqual(result.Migrations, want) {
				t.Fatalf("EditAgent() migrations = %#v, want %#v", result.Migrations, want)
			}
			requireNoInstalledFolder(t, cursor, "mine")
			requireSkillFile(t, agents, "mine", "---\nname: mine\ndescription: mine\n---\n# mine\n")
			if scopes[global].Directory != agents || scopes[global].SharedOnly || !scopes[library.GlobalScope(library.AgentCodex)].SharedOnly {
				t.Fatalf("ListSkillScopes(mine) = %#v; want Cursor's own install in %s, read by Codex too", scopes, agents)
			}
		})
	}
}

// 编辑时去掉第 1 个目录又不迁移:这一层级的组安装去掉,只靠这个组留在另一个目录的成员改为直接安装;
// 第 1 个目录里只靠组留着的成员记录转给还在读那里的 Codex。
func TestEditAgentDroppingTheFirstDirectoryKeepsGroupOnlyInstallsAsDirect(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib, agents, cursor := cursorHome(t)
	global, codex := library.GlobalScope("cursor"), library.GlobalScope(library.AgentCodex)
	first := fetchNamedSkill(t, lib, "first")
	second, _ := fetchLocalSkill(t, lib, "second", "v1")
	writeLocalSkill(t, filepath.Join(cursor, "second"), "v1")
	listUnmanagedSkills(t, lib)
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []library.Skill{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, member.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.InstallGroup(ctx, group.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Uninstall(ctx, second.ID, global, library.UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if scopes := skillScopesOf(t, lib, second.ID); scopes[global].Direct || scopes[global].Directory != cursor {
		t.Fatalf("ListSkillScopes(second) = %#v; want it kept only by the group in %s", scopes, cursor)
	}
	if _, err := lib.EditAgent(ctx, "cursor", library.AgentInput{Name: "Cursor", GlobalDirectories: []string{cursor}, ProjectDirectories: []string{".agents/skills"}}, library.EditAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want the group install of the dropped directory gone", installs, err)
	}
	if scopes := skillScopesOf(t, lib, second.ID); !scopes[global].Direct || len(scopes[global].Groups) != 0 || scopes[global].Directory != cursor {
		t.Fatalf("ListSkillScopes(second) = %#v; want a direct install in %s", scopes, cursor)
	}
	scopes := skillScopesOf(t, lib, first.ID)
	if _, ok := scopes[global]; ok || !scopes[codex].Direct || scopes[codex].SharedOnly {
		t.Fatalf("ListSkillScopes(first) = %#v; want it handed over to Codex", scopes)
	}
	requireInstalledFolder(t, agents, "first", "# first\n")
}

// 移除用户改过目录的 Claude Code:它回到候选池,改过的目录保留;直接安装和组安装撤掉,独占的文件夹按选项删除或保留,
// 别的 Agent 还在读的保留并转交。
func TestDeleteAgentMovesItBackToThePoolAsEdited(t *testing.T) {
	t.Parallel()
	for _, policy := range []library.FolderPolicy{library.Remove, library.Keep} {
		t.Run(string(policy), func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			setTestHome(t, home)
			lib := openTestLibrary(t)
			claudeDir, mineDir := filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".mine", "skills")
			edited, err := lib.EditAgent(ctx, library.AgentClaudeCode, library.AgentInput{
				Name: "Claude", GlobalDirectories: []string{claudeDir, mineDir}, ProjectDirectories: []string{".claude/skills"},
			}, library.EditAgentOptions{})
			if err != nil {
				t.Fatal(err)
			}
			other := addAgent(t, lib, home, "Other", ".claude/skills", ".other/skills")
			claude := library.GlobalScope(library.AgentClaudeCode)
			writeUnmanagedSkill(t, mineDir, "own", "only claude reads it")
			fetched, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: claude, Directory: mineDir, Name: "own"})
			if err != nil {
				t.Fatal(err)
			}
			own, shared := fetched.Skill, fetchNamedSkill(t, lib, "shared")
			if err := lib.Install(ctx, shared.ID, claude, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			group, err := lib.CreateGroup(ctx, "g")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lib.InstallGroup(ctx, group.ID, claude, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			result, err := lib.DeleteAgent(ctx, library.AgentClaudeCode, library.DeleteAgentOptions{Folders: policy})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Kept) != 1 || result.Kept[0].SkillID != shared.ID || !reflect.DeepEqual(result.Kept[0].SharedWith, []library.AgentCode{other.Code}) {
				t.Fatalf("DeleteAgent() kept = %#v; want the shared folder kept for Other", result.Kept)
			}
			if policy == library.Remove {
				requireNoInstalledFolder(t, mineDir, "own")
			} else {
				requireSkillFile(t, mineDir, "own", "---\nname: own\ndescription: only claude reads it\n---\n# own\n")
			}
			requireInstalledFolder(t, claudeDir, "shared", "# shared\n")
			if scopes := skillScopesOf(t, lib, own.ID); len(scopes) != 0 {
				t.Fatalf("ListSkillScopes(own) = %#v; want none", scopes)
			}
			if scopes := skillScopesOf(t, lib, shared.ID); len(scopes) != 1 || !scopes[library.GlobalScope(other.Code)].Direct {
				t.Fatalf("ListSkillScopes(shared) = %#v; want only Other", scopes)
			}
			if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
				t.Fatalf("ListGroupInstalls() = %#v, %v; want none, Other's directories differ", installs, err)
			}
			if slices.ContainsFunc(listAgents(t, lib), func(agent library.Agent) bool { return agent.Code == library.AgentClaudeCode }) {
				t.Fatal("Claude Code still added after DeleteAgent")
			}
			if pooled := poolAgent(t, lib, library.AgentClaudeCode); pooled.Name != "Claude" || !reflect.DeepEqual(pooled.GlobalDirectories, edited.Agent.GlobalDirectories) {
				t.Fatalf("Claude Code in the pool = %#v; want it as edited", pooled)
			}
		})
	}
}

// 移除持有组安装、目录列表与另一个已添加 Agent 完全相同的 Agent:组安装转给那个 Agent,成员照常装着。
func TestDeleteAgentHandsGroupInstallsToAnAgentWithTheSameDirectories(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	addFromPool(t, lib, "cline")
	member := fetchNamedSkill(t, lib, "member")
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.DeleteAgent(ctx, library.AgentCodex, library.DeleteAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	cline := library.GlobalScope("cline")
	installs, err := lib.ListGroupInstalls(ctx)
	if err != nil || len(installs) != 1 || installs[0].GroupID != group.ID || installs[0].Scope != cline {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want the group install handed to Cline", installs, err)
	}
	requireInstalledFolder(t, filepath.Join(home, ".agents", "skills"), "member", "# member\n")
	scopes := skillScopesOf(t, lib, member.ID)
	if len(scopes) != 1 || scopes[cline].Direct || !reflect.DeepEqual(scopes[cline].Groups, []library.GroupID{group.ID}) || scopes[cline].SharedOnly {
		t.Fatalf("ListSkillScopes(member) = %#v; want Cline holding it through the group", scopes)
	}
}

// 移除或改目录的 Agent 的文件夹,另一个 Agent 的目录列表里也有、它自己却在别的目录装着这个技能:
// 那个 Agent 不接收记录,但还在读这个目录,所以文件夹保留;迁移时是复制。
func TestAgentFolderReadByAnAgentThatRecognizesAnotherPlaceIsKept(t *testing.T) {
	t.Parallel()
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete", true: "edit and migrate"}[edit], func(t *testing.T) {
			ctx := t.Context()
			lib, agents, cursor := cursorHome(t)
			writeUnmanagedSkill(t, cursor, "foo", "foo")
			fetched, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: library.GlobalScope("cursor"), Directory: cursor, Name: "foo"})
			if err != nil {
				t.Fatal(err)
			}
			codex := library.GlobalScope(library.AgentCodex)
			if err := lib.Install(ctx, fetched.Skill.ID, codex, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			if edit {
				result, err := lib.EditAgent(ctx, library.AgentCodex, library.AgentInput{
					Name: "Codex", GlobalDirectories: []string{"~/.codex/skills"}, ProjectDirectories: []string{".agents/skills"},
				}, library.EditAgentOptions{Migrate: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Migrations) != 1 || result.Migrations[0].Outcome != library.MigrationCopied {
					t.Fatalf("EditAgent() migrations = %#v; want the folder copied", result.Migrations)
				}
			} else {
				result, err := lib.DeleteAgent(ctx, library.AgentCodex, library.DeleteAgentOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Kept) != 1 || result.Kept[0].Scope != codex || !reflect.DeepEqual(result.Kept[0].SharedWith, []library.AgentCode{"cursor"}) {
					t.Fatalf("DeleteAgent() kept = %#v; want the folder kept for Cursor", result.Kept)
				}
			}
			requireSkillFile(t, agents, "foo", "---\nname: foo\ndescription: foo\n---\n# foo\n")
			if scopes := skillScopesOf(t, lib, fetched.Skill.ID); scopes[library.GlobalScope("cursor")].Directory != cursor {
				t.Fatalf("ListSkillScopes(foo) = %#v; want Cursor still on its own install in %s", scopes, cursor)
			}
		})
	}
}

// 填的几个全局技能目录经符号链接是同一个目录或互相包含时,和字面上重复一样返回 InvalidArgument。
func TestAgentDirectoriesThatAliasEachOtherAreRejected(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	skills := filepath.Join(home, ".skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(skills, filepath.Join(home, ".alias")); err != nil {
		t.Fatal(err)
	}
	for _, directories := range [][]string{{"~/.skills", "~/.alias"}, {"~/.skills", "~/.alias/inner"}} {
		_, err := lib.AddAgent(t.Context(), library.AgentInput{Name: "Mine", GlobalDirectories: directories, ProjectDirectories: []string{".mine/skills"}})
		requireLibraryError(t, err, library.InvalidArgument)
	}
}

// 大小写不敏感的文件系统上,只差大小写的几个技能目录是同一个目录:项目目录、还不存在的全局目录都和字面上重复一样返回 InvalidArgument。
// 区分大小写的文件系统上跳过。
func TestAgentDirectoriesThatDifferOnlyInCaseAreRejected(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	setTestHome(t, home)
	if info, err := os.Stat(strings.ToUpper(home)); err != nil || !info.IsDir() {
		t.Skip("the file system is case-sensitive")
	}
	lib := openTestLibrary(t)
	for _, input := range []library.AgentInput{
		{Name: "Claude Code", GlobalDirectories: []string{"~/.claude/skills"}, ProjectDirectories: []string{".agents/skills", ".Agents/skills"}},
		{Name: "Claude Code", GlobalDirectories: []string{"~/.MyAgent/skills", "~/.myagent/skills"}, ProjectDirectories: []string{".claude/skills"}},
		{Name: "Claude Code", GlobalDirectories: []string{"~/.MyAgent", "~/.myagent/skills"}, ProjectDirectories: []string{".claude/skills"}},
	} {
		_, err := lib.EditAgent(t.Context(), library.AgentClaudeCode, input, library.EditAgentOptions{})
		requireLibraryError(t, err, library.InvalidArgument)
	}
}
