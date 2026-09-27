package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 用项目 Scope 安装:文件夹落在该 Registered project 根下 Resolve 出的目录;Uninstall 后
// 目录与记录一起消失,Library 里的 skill 仍在;再次 Uninstall 得到 NotFound。
func TestInstallAndUninstallProjectScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if directory != filepath.Join(project.Path, ".claude", "skills") {
		t.Fatalf("Resolve(%#v) = %q; want under registered project root", scope, directory)
	}
	target := filepath.Join(directory, skill.Name)
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("project installation missing: %v", err)
	}
	installations, err := lib.ListInstallations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Installation{{
		SkillID:   skill.ID,
		Scope:     scope,
		Directory: directory,
		Direct:    true,
	}}
	if !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() = %#v, want %#v", installations, want)
	}

	if err := lib.Uninstall(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstalled directory still exists: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("Uninstall removed library skill: %v", err)
	}
	if installations, err := lib.ListInstallations(ctx); err != nil || len(installations) != 0 {
		t.Fatalf("ListInstallations() after uninstall = %#v, %v", installations, err)
	}
	if err := lib.Uninstall(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Uninstall() not-installed skill error = %v, want NotFound", err)
	}
}

// 整组安装在身份路径上仍是原子的:某成员目标被外来文件夹占用时整组拒绝,不留半组安装项。
// 项目 Scope 上有未记录文件夹的成员被跳过,其余成员照常安装,组安装照常成立。
func TestInstallGroupOnIdentityScopeSkipsUntrackedMember(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := fetchExampleSkill(t, lib, "first")
	second := fetchExampleSkill(t, lib, "second")
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentCodex, project.ID)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, second.Name, "manual.txt"), []byte("manual"), 0o644)

	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: first.ID, Scope: scope, Outcome: library.MemberInstalled},
		skippedMember(second.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(directory, second.Name)}),
	})
	requireInstallation(t, directory, first.Name, "# first\n")
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skipped member was installed: %v", err)
	}
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{{SkillID: first.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{group.ID}}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() = %#v, %v; want %#v", installations, err, want)
	}
	installs, err := lib.ListGroupInstalls(ctx)
	if want := []library.GroupInstall{{GroupID: group.ID, Scope: scope}}; err != nil || !reflect.DeepEqual(withoutGroupCreatedAt(installs), want) {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want %#v", installs, err, want)
	}
}

// 同一 skill 可同时占用两个 Agent 的全局 Scope,以及同一 Agent 的全局与项目 Scope:各是一个安装项。
func TestIdentityScopesOfOneSkillAreIndependentInstallations(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope(library.AgentClaudeCode),
		library.GlobalScope(library.AgentCodex),
		library.ProjectScope(library.AgentClaudeCode, project.ID),
	}
	for _, scope := range scopes {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatalf("Install(%#v) = %v", scope, err)
		}
	}
	installations, err := lib.ListInstallations(ctx)
	if err != nil || len(installations) != len(scopes) {
		t.Fatalf("ListInstallations() = %#v, %v; want %d installations", installations, err, len(scopes))
	}
	for _, scope := range scopes {
		directory, err := lib.Resolve(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("installation for %#v missing: %v", scope, err)
		}
	}
	// 撤销其中一条不影响其余两条。
	if err := lib.Uninstall(ctx, skill.ID, scopes[0]); err != nil {
		t.Fatal(err)
	}
	installations, err = lib.ListInstallations(ctx)
	if err != nil || len(installations) != 2 {
		t.Fatalf("ListInstallations() after one Uninstall = %#v, %v; want 2", installations, err)
	}
	for _, installation := range installations {
		if installation.Scope == scopes[0] {
			t.Fatalf("uninstalled scope still listed: %#v", installation)
		}
	}
}

// 两个不同身份的 Scope 解析到同一目录(把 $HOME 本身登记为项目)时,第二次安装不能悄悄改写
// 第一个安装项的身份:得到 AlreadyExists,原记录与磁盘保持原样。
func TestInstallDoesNotOverwriteAnotherScopeSharingTheDirectory(t *testing.T) {
	ctx := t.Context()
	// RegisterProject 会解析符号链接(macOS 的 /var → /private/var),HOME 先取规范路径以便两边一致。
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	directory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if projectDirectory, err := lib.Resolve(ctx, projectScope); err != nil || projectDirectory != directory {
		t.Fatalf("precondition: scopes should share a directory, got %q vs %q (%v)", projectDirectory, directory, err)
	}

	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []library.InstallOptions{{}, {OnConflict: library.Overwrite}} {
		err := lib.Install(ctx, skill.ID, projectScope, opts)
		requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: directory})
	}
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{{SkillID: skill.ID, Scope: global, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() = %#v, %v; want %#v", installations, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("global installation lost: %v", err)
	}
}

// 两个身份争同一实际目录时,并发 Install 也不能绕过占用保护:最多一条记录,磁盘上的安装项仍在。
func TestInstallSerializesSharedDirectoryClaims(t *testing.T) {
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchExampleSkill(t, lib, "example-skill")
	scopes := []library.Scope{
		library.GlobalScope(library.AgentCodex),
		library.ProjectScope(library.AgentCodex, project.ID),
	}
	start := make(chan struct{})
	results := make([]error, len(scopes))
	var calls sync.WaitGroup
	for i, scope := range scopes {
		calls.Go(func() {
			<-start
			results[i] = lib.Install(ctx, skill.ID, scope, library.InstallOptions{})
		})
	}
	close(start)
	calls.Wait()
	installations, err := lib.ListInstallations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(installations) != 1 {
		t.Fatalf("ListInstallations() = %#v, install errors=%v; want exactly one claim", installations, results)
	}
	directory, err := lib.Resolve(ctx, installations[0].Scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("surviving installation missing: %v", err)
	}
	var accepted, rejected int
	for _, err := range results {
		switch {
		case err == nil:
			accepted++
		case library.IsCode(err, library.FailedPrecondition):
			rejected++
		default:
			t.Fatalf("Install = %v, want nil or FailedPrecondition", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("Install results = %v, want one success and one FailedPrecondition", results)
	}
}

// 同名不同 Source 的 A 已安装到某 Scope 时,安装 B 不能接管那个文件夹:两种冲突策略都得到
// FailedPrecondition 并指认占用者;A 的记录与内容原样保留,不会出现两条记录指向同一文件夹。
func TestInstallRejectsNameClaimedByAnotherSkill(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := fetchSkillWithBody(t, lib, "commit", "# A\n")
	second := fetchSkillWithBody(t, lib, "commit", "# B\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(directory, "commit")
	for _, opts := range []library.InstallOptions{{}, {OnConflict: library.Overwrite}} {
		err := lib.Install(ctx, second.ID, scope, opts)
		requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID})
	}
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() = %#v, %v; want %#v", installations, err, want)
	}
	requireInstallation(t, directory, "commit", "# A\n")

	// 换用 B 的正确路径:先撤 A,再安装 B。
	if err := lib.Uninstall(ctx, first.ID, scope); err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, second.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInstallation(t, directory, "commit", "# B\n")
	installations, err = lib.ListInstallations(ctx)
	want = []library.Installation{{SkillID: second.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() after switch = %#v, %v; want %#v", installations, err, want)
	}
}

// 占用者只由组安装撑着(无直接安装)时同样受保护;另一组向该 Scope 安装时只跳过被占名字的成员,
// 其他成员照常安装。占用者撤走后再次安装该组即可补齐。
func TestInstallGroupSkipsMemberWhoseNameIsClaimedByAnotherSkill(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := fetchSkillWithBody(t, lib, "commit", "# A\n")
	second := fetchSkillWithBody(t, lib, "commit", "# B\n")
	bystander := fetchSkillWithBody(t, lib, "bystander", "# bystander\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	firstGroup, err := lib.CreateGroup(ctx, "holders")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, firstGroup.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, firstGroup.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(directory, "commit")
	err = lib.Install(ctx, second.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID})

	secondGroup, err := lib.CreateGroup(ctx, "challengers")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{bystander, second} {
		if _, err := lib.AddSkillToGroup(ctx, secondGroup.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	results, err := lib.InstallGroup(ctx, secondGroup.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(second.ID, scope, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID}),
		{SkillID: bystander.ID, Scope: scope, Outcome: library.MemberInstalled},
	})
	requireInstallation(t, directory, "bystander", "# bystander\n")
	requireInstallation(t, directory, "commit", "# A\n")
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{
		{SkillID: first.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{firstGroup.ID}},
		{SkillID: bystander.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{secondGroup.ID}},
	}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() = %#v, %v; want %#v", installations, err, want)
	}

	if err := lib.UninstallGroup(ctx, firstGroup.ID, scope); err != nil {
		t.Fatal(err)
	}
	results, err = lib.InstallGroup(ctx, secondGroup.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: second.ID, Scope: scope, Outcome: library.MemberInstalled},
		{SkillID: bystander.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled},
	})
	requireInstallation(t, directory, "commit", "# B\n")
}

// 占用以实际目录为准:A 在全局 Scope,把 $HOME 登记为项目后向项目 Scope 安装同名的 B 同样被拒;
// 而 A、B 安装到不同目录的 Scope 互不干扰。即使 A 的文件夹已被手删,只要记录在,B 仍被拒。
func TestSameNameSkillsCollideOnlyInTheSameDirectory(t *testing.T) {
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	first := fetchSkillWithBody(t, lib, "commit", "# A\n")
	second := fetchSkillWithBody(t, lib, "commit", "# B\n")
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	sameDirectory := library.ProjectScope(library.AgentCodex, project.ID)
	otherAgent := library.GlobalScope(library.AgentClaudeCode)
	directory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, first.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	err = lib.Install(ctx, second.ID, sameDirectory, library.InstallOptions{OnConflict: library.Overwrite})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(directory, "commit"), Name: "commit", SkillID: first.ID})

	if err := lib.Install(ctx, second.ID, otherAgent, library.InstallOptions{}); err != nil {
		t.Fatalf("Install(B) into another agent's directory = %v, want success", err)
	}
	otherDirectory, err := lib.Resolve(ctx, otherAgent)
	if err != nil {
		t.Fatal(err)
	}
	requireInstallation(t, directory, "commit", "# A\n")
	requireInstallation(t, otherDirectory, "commit", "# B\n")
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{
		{SkillID: first.ID, Scope: global, Directory: directory, Direct: true},
		{SkillID: second.ID, Scope: otherAgent, Directory: otherDirectory, Direct: true},
	}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() = %#v, %v; want %#v", installations, err, want)
	}

	if err := os.RemoveAll(filepath.Join(directory, "commit")); err != nil {
		t.Fatal(err)
	}
	err = lib.Install(ctx, second.ID, global, library.InstallOptions{})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(directory, "commit"), Name: "commit", SkillID: first.ID})
}

// 身份 Scope 里的错误指认在安装时同样得到 NotFound,不会写到任何目录。
func TestInstallUnknownAgentOrProjectIsNotFound(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")

	for name, scope := range map[string]library.Scope{
		"unknown agent":        library.GlobalScope("cursor"),
		"unregistered project": library.ProjectScope(library.AgentClaudeCode, 42),
	} {
		t.Run(name, func(t *testing.T) {
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Install(%#v) = %v, want NotFound", scope, err)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed Install wrote into HOME: %v, %v", entries, err)
	}
}
