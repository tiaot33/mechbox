package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
	"github.com/tiaot33/mechbox/internal/library/platformtest"
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

// 填的几个全局技能目录经符号链接是同一个目录或互相包含时,和字面上重复一样返回 InvalidArgument,
// Details.Paths 是重复的那两个目录(界面据此说明,自己查不出经符号链接的重复)。
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
		want := []string{filepath.Join(home, directories[0][2:]), filepath.Join(home, directories[1][2:])}
		if got := library.ErrorOf(err).Details; got.Field != "global_directories" || !slices.Equal(got.Paths, want) {
			t.Errorf("AddAgent(%v) details = %+v; want global_directories %v", directories, got, want)
		}
	}
	_, err := lib.AddAgent(t.Context(), library.AgentInput{Name: "Mine", GlobalDirectories: []string{"~/.mine/skills"}, ProjectDirectories: []string{".a/skills", ".a"}})
	if got := library.ErrorOf(err).Details; !slices.Equal(got.Paths, []string{".a/skills", ".a"}) {
		t.Errorf("AddAgent with nested project directories: details = %+v", got)
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

// 大小写不敏感的文件系统上只改目录的大小写:是同一个目录,安装照常算已装、文件夹不动,迁移也不搬。区分大小写的文件系统上跳过。
func TestEditAgentChangingOnlyTheCaseOfADirectoryKeepsItsInstalls(t *testing.T) {
	t.Parallel()
	for _, migrate := range []bool{false, true} {
		home := t.TempDir()
		if info, err := os.Stat(strings.ToUpper(home)); err != nil || !info.IsDir() {
			t.Skip("the file system is case-sensitive")
		}
		setTestHome(t, home)
		lib := openTestLibrary(t)
		ctx := t.Context()
		foo := fetchNamedSkill(t, lib, "foo")
		codex := library.GlobalScope(library.AgentCodex)
		if err := lib.Install(ctx, foo.ID, codex, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
		agents := filepath.Join(home, ".agents", "skills")
		result, err := lib.EditAgent(ctx, library.AgentCodex, library.AgentInput{Name: "Codex", GlobalDirectories: []string{"~/.Agents/skills"}, ProjectDirectories: []string{".agents/skills"}}, library.EditAgentOptions{Migrate: migrate})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Migrations) != 0 {
			t.Fatalf("migrate=%v: Migrations = %#v; want none", migrate, result.Migrations)
		}
		scopes := skillScopesOf(t, lib, foo.ID)
		if got, ok := scopes[codex]; !ok || got.SharedOnly || got.Directory != filepath.Join(home, ".Agents", "skills") {
			t.Fatalf("migrate=%v: ListSkillScopes(foo) = %#v; want Codex still installed in the renamed directory", migrate, scopes)
		}
		requireInstalledFolder(t, agents, "foo", "# foo\n")
	}
}

// 大小写不敏感的文件系统上,Codex 和 Cline 共用 ~/.agents/skills,Codex 只把目录改成 ~/.Agents/skills:两者仍读同一个目录。
// Cline 照常算已装、不把文件夹列成未纳管;之后移除 Codex 并删除技能文件夹,Cline 还在读的文件夹保留,记录与组安装转给 Cline。
// 区分大小写的文件系统上跳过。
func TestAgentsSharingADirectoryStillShareItAfterOneChangesOnlyItsCase(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	if info, err := os.Stat(strings.ToUpper(home)); err != nil || !info.IsDir() {
		t.Skip("the file system is case-sensitive")
	}
	setTestHome(t, home)
	lib := openTestLibrary(t)
	addFromPool(t, lib, "cline")
	foo := fetchNamedSkill(t, lib, "foo")
	member := fetchNamedSkill(t, lib, "member")
	codex, cline := library.GlobalScope(library.AgentCodex), library.GlobalScope("cline")
	if err := lib.Install(ctx, foo.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.EditAgent(ctx, library.AgentCodex, library.AgentInput{Name: "Codex", GlobalDirectories: []string{"~/.Agents/skills"}, ProjectDirectories: []string{".agents/skills"}}, library.EditAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	scopes := skillScopesOf(t, lib, foo.ID)
	if got, ok := scopes[cline]; !ok || !got.SharedOnly || !slices.Contains(got.SharedWith, library.AgentCodex) {
		t.Fatalf("ListSkillScopes(foo) = %#v; want Cline reading Codex's install", scopes)
	}
	unmanaged, err := lib.ListUnmanagedSkills(ctx)
	if err != nil || len(unmanaged) != 0 {
		t.Fatalf("ListUnmanagedSkills() = %#v, %v; want none", unmanaged, err)
	}
	if _, err := lib.DeleteAgent(ctx, library.AgentCodex, library.DeleteAgentOptions{Folders: library.Remove}); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(home, ".agents", "skills")
	requireInstalledFolder(t, agents, "foo", "# foo\n")
	requireInstalledFolder(t, agents, "member", "# member\n")
	if scopes := skillScopesOf(t, lib, foo.ID); len(scopes) != 1 || scopes[cline].SharedOnly {
		t.Fatalf("ListSkillScopes(foo) = %#v; want the install handed to Cline", scopes)
	}
	installs, err := lib.ListGroupInstalls(ctx)
	if err != nil || len(installs) != 1 || installs[0].Scope != cline {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want the group install handed to Cline", installs, err)
	}
}

// 主目录在不区分大小写的卷上、项目在区分大小写的发行版里:只改项目目录的大小写,在那个项目里是另一个目录。
// 不迁移时那里的安装撤销,不再记在一个不存在的目录里。
func TestEditAgentChangingOnlyTheCaseOfAProjectDirectoryOnACaseSensitiveVolumeIsAMove(t *testing.T) {
	home := t.TempDir()
	if info, err := os.Stat(strings.ToUpper(home)); err != nil || !info.IsDir() {
		t.Skip("the file system is case-sensitive")
	}
	setTestHome(t, home)
	platformtest.EmulateWSL(t, "sh")
	lib := openTestLibrary(t)
	ctx := t.Context()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, wslPath(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	foo := fetchNamedSkill(t, lib, "foo")
	scope := library.ProjectScope(library.AgentCodex, project.ID)
	if err := lib.Install(ctx, foo.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.EditAgent(ctx, library.AgentCodex, library.AgentInput{Name: "Codex", GlobalDirectories: []string{"~/.agents/skills"}, ProjectDirectories: []string{".Agents/skills"}}, library.EditAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	if scopes := skillScopesOf(t, lib, foo.ID); len(scopes) != 0 {
		t.Fatalf("ListSkillScopes(foo) = %#v; want the install in the project dropped", scopes)
	}
	requireInstalledFolder(t, filepath.Join(dir, ".agents", "skills"), "foo", "# foo\n")
}

// HomeFoldsCase 和编辑 Agent 时的判断一致:为 true 时只改全局目录的大小写,安装照常算已装;为 false 时算去掉了那个目录。
// 主目录在 WSL 发行版里时总是 false。
func TestHomeFoldsCaseIsHowEditingAgentsComparesGlobalDirectories(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	ctx := t.Context()
	foo := fetchNamedSkill(t, lib, "foo")
	codex := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, foo.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	folds := lib.HomeFoldsCase()
	if _, err := lib.EditAgent(ctx, library.AgentCodex, library.AgentInput{Name: "Codex", GlobalDirectories: []string{"~/.Agents/skills"}, ProjectDirectories: []string{".agents/skills"}}, library.EditAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, kept := skillScopesOf(t, lib, foo.ID)[codex]; kept != folds {
		t.Fatalf("HomeFoldsCase() = %v but the install kept after changing only the case = %v", folds, kept)
	}
}

func TestHomeFoldsCaseIsFalseInAWSLDistro(t *testing.T) {
	lib, _ := openWSLLibrary(t)
	if lib.HomeFoldsCase() {
		t.Fatal("HomeFoldsCase() in a WSL distro = true; want false")
	}
}

// 新目录里别的 Agent 为这个技能装的那一处不是能读的技能文件夹(指向已删除位置的符号链接、普通文件、没有 SKILL.md):
// 不算这个 Agent 已经装着,结果写读不了、那一份是 Codex 装的;不覆盖、不搬,旧文件夹留在原处,这个 Agent 自己的安装记录去掉,
// 只经共用目录列着那一处(结果框据 InstalledFor 不说它不再算已装)。
// 以前只看名字在不在,结果写「新目录里已经装着」,这个 Agent 照常算已装,实际读不到。
func TestEditAgentMigrationDoesNotAdoptABrokenFolderInTheNewDirectory(t *testing.T) {
	t.Parallel()
	breakers := map[string]func(t *testing.T, folder string){
		"dangling link": func(t *testing.T, folder string) {
			if err := os.RemoveAll(folder); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(filepath.Dir(folder), "deleted"), folder); err != nil {
				t.Fatal(err)
			}
		},
		"plain file": func(t *testing.T, folder string) {
			if err := os.RemoveAll(folder); err != nil {
				t.Fatal(err)
			}
			writeFile(t, folder, []byte("not a folder\n"), 0o644)
		},
		"no SKILL.md": func(t *testing.T, folder string) {
			if err := os.Remove(filepath.Join(folder, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		},
		// 里面有符号链接时读到它就停,不能据此当成能读:还要看 SKILL.md 在不在。
		"no SKILL.md but a link inside": func(t *testing.T, folder string) {
			if err := os.Remove(filepath.Join(folder, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc/hosts", filepath.Join(folder, "hosts")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, breakFolder := range breakers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			home := t.TempDir()
			setTestHome(t, home)
			lib := openTestLibrary(t)
			agent := addAgent(t, lib, home, "Agent", ".agent/skills", ".agent/skills")
			skill := fetchNamedSkill(t, lib, "foo")
			global := library.GlobalScope(agent.Code)
			for _, scope := range []library.Scope{library.GlobalScope(library.AgentCodex), global} {
				if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			oldDir, newDir := filepath.Join(home, ".agent", "skills"), filepath.Join(home, ".agents", "skills")
			breakFolder(t, filepath.Join(newDir, "foo"))
			migrations := editMigrating(t, lib, agent, newDir)
			want := []library.AgentMigration{{
				SkillID: skill.ID, Scope: global, Outcome: library.MigrationUnreadable,
				From: filepath.Join(oldDir, "foo"), To: filepath.Join(newDir, "foo"), InstalledFor: []library.AgentCode{library.AgentCodex},
			}}
			if !reflect.DeepEqual(migrations, want) {
				t.Fatalf("EditAgent() migrations = %#v, want %#v", migrations, want)
			}
			requireInstalledFolder(t, oldDir, "foo", "# foo\n")
			scopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{Scope: &global})
			if err != nil || len(scopes) != 1 || !scopes[0].SharedOnly || scopes[0].Folder != filepath.Join(newDir, "foo") {
				t.Fatalf("ListSkillScopes(agent) = %#v, %v; want only Codex's folder, through the shared directory", scopes, err)
			}
		})
	}
}

// 库里有两个同名、内容逐字节相同的技能(来自两个来源),新目录里没有记录的那一份和这个 Agent 装的内容相同:算已装在那里,
// 不论这个 Agent 装的是哪一个。以前按内容认出的可能是另一个技能,结果写没有迁移,这个 Agent 的安装记录被删掉。
func TestEditAgentMigrationAdoptsAnIdenticalFolderWhenAnotherSkillHasTheSameContent(t *testing.T) {
	t.Parallel()
	for _, which := range []int{0, 1} {
		t.Run([]string{"first", "second"}[which], func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			home := t.TempDir()
			setTestHome(t, home)
			lib := openTestLibrary(t)
			agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
			skills := []library.Skill{fetchNamedSkill(t, lib, "foo"), fetchNamedSkill(t, lib, "foo")}
			if skills[0].ID == skills[1].ID {
				t.Fatal("fetchNamedSkill() twice gave the same skill; want two skills from two sources")
			}
			global := library.GlobalScope(agent.Code)
			if err := lib.Install(ctx, skills[which].ID, global, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			oldDir, newDir := filepath.Join(home, ".old", "skills"), filepath.Join(home, ".new", "skills")
			if err := os.CopyFS(filepath.Join(newDir, "foo"), os.DirFS(filepath.Join(oldDir, "foo"))); err != nil {
				t.Fatal(err)
			}
			migrations := editMigrating(t, lib, agent, newDir)
			want := []library.AgentMigration{{SkillID: skills[which].ID, Scope: global, Outcome: library.MigrationAlreadyInstalled, From: filepath.Join(oldDir, "foo"), To: filepath.Join(newDir, "foo")}}
			if !reflect.DeepEqual(migrations, want) {
				t.Fatalf("EditAgent() migrations = %#v, want %#v", migrations, want)
			}
			requireOwnInstall(t, lib, global, filepath.Join(newDir, "foo"))
		})
	}
}

// 新目录里同名文件夹的 SKILL.md 没有读权限(如别的用户建的):结果写读不了,不当作别的同名技能;不覆盖、不搬,旧文件夹留在原处,
// 这个 Agent 在这一处不再算已装。以前读不出来就当成别的技能,结果只写没有迁移,看不出原因。
func TestEditAgentMigrationReportsAnUnreadableFolderInTheNewDirectory(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("root reads any file, and mode 0 doesn't stop reading on Windows")
	}
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "foo")
	global := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	oldDir, newDir := filepath.Join(home, ".old", "skills"), filepath.Join(home, ".new", "skills")
	writeFile(t, filepath.Join(newDir, "foo", "SKILL.md"), []byte(skillDocument("foo", "# foo\n")), 0)
	migrations := editMigrating(t, lib, agent, newDir)
	want := []library.AgentMigration{{SkillID: skill.ID, Scope: global, Outcome: library.MigrationUnreadable, From: filepath.Join(oldDir, "foo"), To: filepath.Join(newDir, "foo")}}
	if !reflect.DeepEqual(migrations, want) {
		t.Fatalf("EditAgent() migrations = %#v, want %#v", migrations, want)
	}
	requireInstalledFolder(t, oldDir, "foo", "# foo\n")
	if scopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{Scope: &global}); err != nil || len(scopes) != 0 {
		t.Fatalf("ListSkillScopes(agent) = %#v, %v; want none", scopes, err)
	}
}

// 同上,全局目录在 WSL 发行版里时也写读不了。以前发行版里读到没有读权限的文件时整段命令出错,整个编辑报内部错误。
func TestWSLEditAgentMigrationReportsAnUnreadableFolderInTheNewDirectory(t *testing.T) {
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("root reads any file, and mode 0 doesn't stop reading on Windows")
	}
	lib, home := openWSLLibrary(t)
	ctx := t.Context()
	agent, err := lib.AddAgent(ctx, library.AgentInput{Name: "Agent", GlobalDirectories: []string{wslPath(t, filepath.Join(home, ".old/skills"))}, ProjectDirectories: []string{".agent/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "foo")
	global := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".new/skills/foo/SKILL.md"), []byte(skillDocument("foo", "# foo\n")), 0)
	migrations := editMigrating(t, lib, agent, wslPath(t, filepath.Join(home, ".new/skills")))
	want := []library.AgentMigration{{
		SkillID: skill.ID, Scope: global, Outcome: library.MigrationUnreadable,
		From: wslPath(t, filepath.Join(home, ".old/skills/foo")), To: wslPath(t, filepath.Join(home, ".new/skills/foo")),
	}}
	if !reflect.DeepEqual(migrations, want) {
		t.Fatalf("EditAgent() migrations = %#v, want %#v", migrations, want)
	}
	requireInstalledFolder(t, filepath.Join(home, ".old/skills"), "foo", "# foo\n")
}

// 编辑 Agent 时检测出错(如发行版里的命令一时失败):整次报错,文件夹没搬,Agent 的目录与安装记录都没改;照原样重试就做完。
// 以前先搬文件夹、写进数据库再检测,检测出错时界面报编辑失败、迁移结果也不弹,实际却已经改了。
func TestEditAgentChangesNothingWhenDetectionFails(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestHome(t, home)
	t.Setenv("HOME", home)
	// 充当发行版的 sh 外面套一层:命令里出现 BREAK-DETECTION 时这个会话就断掉,检测那里的目录就会出错。
	shell := filepath.Join(t.TempDir(), "flaky-sh")
	writeFile(t, shell, []byte("#!/bin/sh\nwhile IFS= read -r line; do case \"$line\" in *BREAK-DETECTION*) exit 1;; esac; printf '%s\\n' \"$line\"; done | sh \"$@\"\n"), 0o755)
	platformtest.EmulateWSL(t, shell)
	lib := openTestLibrary(t)
	ctx := t.Context()
	if err := lib.SetWSLDistro(ctx, t.Name()); err != nil {
		t.Fatal(err)
	}
	oldDir, newDir := filepath.Join(home, ".old/skills"), filepath.Join(home, ".new/skills")
	agent, err := lib.AddAgent(ctx, library.AgentInput{Name: "Agent", GlobalDirectories: []string{wslPath(t, oldDir)}, ProjectDirectories: []string{".agent/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "foo")
	global := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	input := library.AgentInput{
		Name: "Agent", GlobalDirectories: []string{wslPath(t, newDir)}, ProjectDirectories: []string{".agent/skills"},
		DetectDirectories: []string{wslPath(t, filepath.Join(home, "BREAK-DETECTION"))},
	}
	if _, err := lib.EditAgent(ctx, agent.Code, input, library.EditAgentOptions{Migrate: true}); err == nil {
		t.Fatal("EditAgent() with a failing detection = nil error; want the failure reported")
	}
	requireInstalledFolder(t, oldDir, "foo", "# foo\n")
	if _, err := os.Lstat(filepath.Join(newDir, "foo")); !os.IsNotExist(err) {
		t.Fatalf("Lstat(new folder) after a failed EditAgent = %v; want nothing moved", err)
	}
	if !hasAgent(listAgents(t, lib), agent) {
		t.Fatalf("ListAgents() after a failed EditAgent = %#v; want %#v unchanged", listAgents(t, lib), agent)
	}
	requireOwnInstall(t, lib, global, wslPath(t, filepath.Join(oldDir, "foo")))

	writeFile(t, filepath.Join(home, ".agent", "settings.json"), []byte("{}\n"), 0o644)
	input.DetectDirectories = []string{wslPath(t, filepath.Join(home, ".agent"))}
	result, err := lib.EditAgent(ctx, agent.Code, input, library.EditAgentOptions{Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Agent.Detected || len(result.Migrations) != 1 || result.Migrations[0].Outcome != library.MigrationMoved {
		t.Fatalf("EditAgent() retried = %#v; want detected and the folder moved", result)
	}
	requireInstalledFolder(t, newDir, "foo", "# foo\n")
}

// 结果里的 Detected 按搬完文件夹之后的样子:检测目录是新技能目录的上一级、本来不在,迁移时建出来的,结果里就是检测到,
// 和紧接着 ListAgents 列出的一致。
func TestEditAgentDetectsAfterMigrating(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "foo")
	if err := lib.Install(ctx, skill.ID, library.GlobalScope(agent.Code), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	input := library.AgentInput{
		Name: "Agent", GlobalDirectories: []string{filepath.Join(home, ".new", "skills")}, ProjectDirectories: []string{".agent/skills"},
		DetectDirectories: []string{filepath.Join(home, ".new")},
	}
	result, err := lib.EditAgent(ctx, agent.Code, input, library.EditAgentOptions{Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Agent.Detected || !hasAgent(listAgents(t, lib), result.Agent) {
		t.Fatalf("EditAgent() agent = %#v; want it detected, as ListAgents lists it", result.Agent)
	}
}

// 别的 Agent 为这个技能装的那一份里有符号链接(读到它就停)、SKILL.md 能读:照常算已经装着。
func TestEditAgentMigrationAdoptsARecordedFolderWithALinkInside(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".agent/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "foo")
	for _, scope := range []library.Scope{library.GlobalScope(library.AgentCodex), library.GlobalScope(agent.Code)} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	newDir := filepath.Join(home, ".agents", "skills")
	if err := os.Symlink("/etc/hosts", filepath.Join(newDir, "foo", "hosts")); err != nil {
		t.Fatal(err)
	}
	migrations := editMigrating(t, lib, agent, newDir)
	if len(migrations) != 1 || migrations[0].Outcome != library.MigrationAlreadyInstalled {
		t.Fatalf("EditAgent() migrations = %#v; want Codex's folder counted as already installed", migrations)
	}
	requireOwnInstall(t, lib, library.GlobalScope(agent.Code), filepath.Join(newDir, "foo"))
}

// 新目录里没有记录的同名项是指向没有读权限的地方的链接:写读不了,不当成别的同名文件夹。
func TestEditAgentMigrationReportsALinkToAnUnreadablePlace(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("root reads any directory, and mode 0 doesn't stop reading on Windows")
	}
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "foo")
	global := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(home, "private")
	writeFile(t, filepath.Join(private, "foo", "SKILL.md"), []byte(skillDocument("foo", "# foo\n")), 0o644)
	if err := os.Chmod(private, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(private, 0o755) })
	newDir := filepath.Join(home, ".new", "skills")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(private, "foo"), filepath.Join(newDir, "foo")); err != nil {
		t.Fatal(err)
	}
	migrations := editMigrating(t, lib, agent, newDir)
	if len(migrations) != 1 || migrations[0].Outcome != library.MigrationUnreadable || len(migrations[0].InstalledFor) != 0 {
		t.Fatalf("EditAgent() migrations = %#v; want unreadable", migrations)
	}
	requireInstalledFolder(t, filepath.Join(home, ".old", "skills"), "foo", "# foo\n")
}

// 大小写不敏感的文件系统上,新目录只和别的 Agent 的目录差大小写时是同一个目录:那里同名的是库里另一个内容相同的技能装的,
// 算被它占着、没有迁移,不把这个技能的安装记到它的文件夹上。以前按字面比较目录,认领了别的技能的文件夹,之后单独卸载这个技能会删掉它。
// 区分大小写的文件系统上跳过。
func TestEditAgentMigrationRespectsAnotherSkillsFolderInADirectoryThatDiffersOnlyInCase(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	if info, err := os.Stat(strings.ToUpper(home)); err != nil || !info.IsDir() {
		t.Skip("the file system is case-sensitive")
	}
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	mine, theirs := fetchNamedSkill(t, lib, "foo"), fetchNamedSkill(t, lib, "foo")
	global := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, mine.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, theirs.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	migrations := editMigrating(t, lib, agent, filepath.Join(home, ".AGENTS", "skills"))
	if len(migrations) != 1 || migrations[0].Outcome != library.MigrationSkipped {
		t.Fatalf("EditAgent() migrations = %#v; want skipped, the folder is another skill's", migrations)
	}
	if scopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{SkillID: mine.ID}); err != nil || len(scopes) != 0 {
		t.Fatalf("ListSkillScopes(mine) = %#v, %v; want none", scopes, err)
	}
}
