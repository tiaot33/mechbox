package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// 在观察到指定成员的磁盘改动后同步取消,不依赖调度时序或内部故障注入接口。
type cancelAfterPlacement struct {
	context.Context
	cancel  context.CancelFunc
	changed func() bool
}

func (c cancelAfterPlacement) Err() error {
	if c.changed() {
		c.cancel()
	}
	return c.Context.Err()
}

func TestGroupMidBatchCancellationRestoresSkillScopesAndOrigins(t *testing.T) {
	t.Parallel()
	for _, uninstall := range []bool{false, true} {
		name := "install"
		if uninstall {
			name = "uninstall"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			setTestHome(t, t.TempDir())
			lib := openTestLibrary(t)
			group, err := lib.CreateGroup(ctx, "batch")
			if err != nil {
				t.Fatal(err)
			}
			scope := library.GlobalScope(library.AgentCodex)
			directory, err := lib.ScopeDirectory(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			var skills []library.Skill
			for _, name := range []string{"first", "second", "direct", "shared"} {
				source := filepath.Join(t.TempDir(), name)
				writeLocalSkill(t, source, name)
				skill, err := fetchLocalDir(ctx, lib, source)
				if err != nil {
					t.Fatal(err)
				}
				skills = append(skills, skill)
				if name != "direct" {
					if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, skill := range skills[2:] {
				if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			other, err := lib.CreateGroup(ctx, "other")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lib.AddSkillToGroup(ctx, other.ID, skills[3].ID); err != nil {
				t.Fatal(err)
			}
			if _, err := lib.InstallGroup(ctx, other.ID, scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			firstPath := filepath.Join(directory, "first", "SKILL.md")
			if uninstall {
				if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				// 首个成员覆盖未记录的文件夹,第二个成员原本没有安装;shared 已安装,不写盘。
				writeFile(t, firstPath, []byte("original local content"), 0o644)
			}
			before, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
			if err != nil {
				t.Fatal(err)
			}
			installsBefore, err := lib.ListGroupInstalls(ctx)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(firstPath)
			if err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(ctx)
			defer cancel()
			fault := cancelAfterPlacement{Context: base, cancel: cancel, changed: func() bool {
				content, err := os.ReadFile(firstPath)
				if uninstall {
					return errors.Is(err, os.ErrNotExist)
				}
				// 已覆盖第一个成员并新增第二个成员,让回滚同时验证恢复与移除。
				content, err = os.ReadFile(filepath.Join(directory, "second", "SKILL.md"))
				return err == nil && string(content) == skillDocument("second", "second")
			}}
			if uninstall {
				err = lib.UninstallGroup(fault, group.ID, scope, library.UninstallOptions{})
			} else {
				_, err = lib.InstallGroup(fault, group.ID, scope, library.InstallOptions{Overwrite: true})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("mid-batch error = %v, want cancellation", err)
			}
			after, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("skill scopes changed: before=%#v after=%#v err=%v", before, after, err)
			}
			installsAfter, err := lib.ListGroupInstalls(ctx)
			if err != nil || !reflect.DeepEqual(installsBefore, installsAfter) {
				t.Fatalf("group installs changed: before=%#v after=%#v err=%v", installsBefore, installsAfter, err)
			}
			content, err := os.ReadFile(firstPath)
			if err != nil || string(content) != string(original) {
				t.Fatalf("first skill scope not restored: %q, %v", content, err)
			}
			for _, skill := range skills[1:] {
				content, err := os.ReadFile(filepath.Join(directory, skill.Name, "SKILL.md"))
				if !uninstall && skill.Name == "second" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("partial skill scope remains: %v", err)
					}
				} else if err != nil || string(content) != skillDocument(skill.Name, skill.Name) {
					t.Fatalf("skill scope %s changed: %q, %v", skill.Name, content, err)
				}
			}
			requireNoStagedEntries(t, directory)
		})
	}
}

func TestGroupConflictPoliciesHandleDanglingSymlinks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setTestHome(t, t.TempDir())
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "example")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, skill.Name)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := os.Symlink(missing, target); err != nil {
		t.Fatal(err)
	}
	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{skippedMember(skill.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: target})})
	if got, err := os.Readlink(target); err != nil || got != missing {
		t.Fatalf("conflict changed: %q, %v", got, err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(content) != skillDocument("example", "example") {
		t.Fatalf("Overwrite = %q, %v", content, err)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(missing, target); err != nil {
		t.Fatal(err)
	}
	if err := lib.UninstallGroup(ctx, group.ID, scope, library.UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left dangling skill scope: %v", err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("skill scopes = %#v, %v", skillScopes, err)
	}
}

func TestGroupRollbackPreservesConcurrentInstall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setTestHome(t, t.TempDir())
	lib := openTestLibrary(t)
	group, err := lib.CreateGroup(ctx, "batch")
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var first library.Skill
	for _, name := range []string{"first", "second"} {
		source := filepath.Join(t.TempDir(), name)
		writeLocalSkill(t, source, name)
		skill, err := fetchLocalDir(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		if first.ID == 0 {
			first = skill
		}
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(directory, first.Name, "SKILL.md")
	writeFile(t, target, []byte("original"), 0o644)
	installed, release := make(chan struct{}), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	defer resume()
	pause := sync.OnceFunc(func() { close(installed); <-release })
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	fault := cancelAfterPlacement{Context: base, cancel: cancel, changed: func() bool {
		content, err := os.ReadFile(target)
		if err != nil || string(content) != skillDocument("first", "first") {
			return false
		}
		pause()
		return true
	}}
	groupDone := make(chan error, 1)
	go func() {
		_, err := lib.InstallGroup(fault, group.ID, scope, library.InstallOptions{Overwrite: true})
		groupDone <- err
	}()
	select {
	case <-installed:
	case <-time.After(5 * time.Second):
		t.Fatal("group never installed first member")
	}
	directDone := make(chan error, 1)
	go func() {
		directDone <- lib.Install(ctx, first.ID, scope, library.InstallOptions{Overwrite: true})
	}()
	// 旧实现允许直接安装在回滚前提交;串行实现则等分组回滚后再完成。
	var directErr error
	finished := false
	select {
	case directErr = <-directDone:
		finished = true
	case <-time.After(200 * time.Millisecond):
	}
	resume()
	if err := <-groupDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("group error = %v", err)
	}
	if !finished {
		directErr = <-directDone
	}
	if directErr != nil {
		t.Fatal(directErr)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != skillDocument("first", "first") {
		t.Fatalf("successful direct install skill scope lost: %q, %v", content, err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{{SkillID: first.ID, Scope: scope, Folder: filepath.Join(directory, first.Name), Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("skill scopes = %#v, %v; want %#v", skillScopes, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, "second")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial group skill scope remains: %v", err)
	}
}

// 空组也能安装:组安装带时间记录下来;之后加入的成员随之安装。成员全部移出(uninstall)后组安装仍在,
// 再次加入又会安装。再次 InstallGroup 不改变组安装的时间。
func TestEmptyGroupInstallIsRecordedAndCoversLaterMembers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setTestHome(t, t.TempDir())
	lib := openTestLibrary(t)
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	results, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, nil)
	end := time.Now().UTC()
	installs, err := lib.ListGroupInstalls(ctx)
	if err != nil || len(installs) != 1 || installs[0].GroupID != g.ID || installs[0].Scope != scope ||
		installs[0].CreatedAt.Before(start) || installs[0].CreatedAt.After(end) {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want %d at %v within [%v, %v]", installs, err, g.ID, scope, start, end)
	}

	skill := fetchNamedSkill(t, lib, "late")
	for range 2 {
		results, err = lib.AddSkillToGroup(ctx, g.ID, skill.ID)
		requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: skill.ID, Scope: scope, Outcome: library.MemberInstalled}})
		requireInstalledFolder(t, directory, "late", "# late\n")
		requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: scope, Folder: filepath.Join(directory, skill.Name), Groups: []library.GroupID{g.ID}}})

		if err := lib.RemoveSkillFromGroup(ctx, g.ID, skill.ID, library.RemoveFromGroupOptions{IfNoOtherInstall: library.UninstallMember}); err != nil {
			t.Fatal(err)
		}
		requireNoInstalledFolder(t, directory, "late")
		requireSkillScopes(t, lib, nil)
	}

	if _, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	again, err := lib.ListGroupInstalls(ctx)
	if err != nil || !reflect.DeepEqual(again, installs) {
		t.Fatalf("ListGroupInstalls() after re-install = %#v, %v; want %#v", again, err, installs)
	}
}

// 作用域根目录(项目为已登记路径)不在本机时 Library 不创建它:Install / InstallGroup 报 FailedPrecondition,
// 不写盘、不留记录、不记组安装;加入成员跳过该 Scope,成员关系照加。根目录存在而 Agent 目录不存在时照常创建。
func TestMissingScopeRootIsNeverCreated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setTestHome(t, t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	// 根目录存在、.claude/skills 还不存在:组安装照常创建 Agent 目录。
	for _, target := range []library.Scope{global, scope} {
		if _, err := lib.InstallGroup(ctx, g.ID, target, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	present := fetchNamedSkill(t, lib, "present")
	if _, err := lib.AddSkillToGroup(ctx, g.ID, present.ID); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, directory, "present", "# present\n")

	// 项目被移走:根目录不存在。
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}
	missing := library.ErrorDetails{Path: project.Path, ProjectID: project.ID}
	skill := fetchNamedSkill(t, lib, "late")
	// 安装计划与执行一致:根目录缺失时同样报 FailedPrecondition。
	_, err = lib.PlanInstall(ctx, skill.ID, scope)
	requireErrorDetails(t, err, library.FailedPrecondition, missing)
	_, err = lib.PlanGroupInstall(ctx, g.ID, scope)
	requireErrorDetails(t, err, library.FailedPrecondition, missing)
	requireErrorDetails(t, lib.Install(ctx, skill.ID, scope, library.InstallOptions{}), library.FailedPrecondition, missing)
	// 先确认技能存在,再查根目录。
	if err := lib.Install(ctx, library.SkillID(9999), scope, library.InstallOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Install(unknown skill) with missing root = %v, want NotFound", err)
	}
	other, err := lib.CreateGroup(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.InstallGroup(ctx, other.ID, scope, library.InstallOptions{})
	requireErrorDetails(t, err, library.FailedPrecondition, missing)

	results, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(skill.ID, scope, library.FailedPrecondition, missing),
		{SkillID: skill.ID, Scope: global, Outcome: library.MemberInstalled},
	})
	if got := groupMembers(t, lib, g.ID); !reflect.DeepEqual(got, []library.SkillID{present.ID, skill.ID}) {
		t.Fatalf("group members = %v, want [%d %d]", got, present.ID, skill.ID)
	}
	if _, err := os.Lstat(project.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing project root was created: %v", err)
	}
	installs, err := lib.ListGroupInstalls(ctx)
	want := []library.GroupInstall{{GroupID: g.ID, Scope: scope}, {GroupID: g.ID, Scope: global}}
	if err != nil || !reflect.DeepEqual(withoutGroupCreatedAt(installs), want) {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want %#v", installs, err, want)
	}
	globalDir, err := lib.ScopeDirectory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	via := []library.GroupID{g.ID}
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: present.ID, Scope: scope, Folder: filepath.Join(directory, present.Name), Groups: via},
		{SkillID: present.ID, Scope: global, Folder: filepath.Join(globalDir, present.Name), Groups: via},
		{SkillID: skill.ID, Scope: global, Folder: filepath.Join(globalDir, skill.Name), Groups: via},
	})
}

// 两个 Agent 的技能目录经符号链接是同一处:往 Claude Code 组安装一个 Codex 已经装在那里的技能,跳过的原因带上这个技能自己和那个文件夹,
// 界面据此说「这个技能的另一个作用域已装在那里」,不再当成项目不在这台机器上。
func TestGroupInstallReportsTheSameSkillInALinkedDirectory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	shared := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "foo")
	if err := lib.Install(ctx, skill.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	results, err := lib.InstallGroup(ctx, group.ID, library.GlobalScope(library.AgentClaudeCode), library.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != library.MemberSkipped || results[0].Error == nil {
		t.Fatalf("InstallGroup() = %#v; want the member skipped", results)
	}
	details := results[0].Error.Details
	if results[0].Error.Code != library.FailedPrecondition || details.SkillID != skill.ID || details.Path != filepath.Join(home, ".claude", "skills", "foo") {
		t.Fatalf("skip error = %#v; want FailedPrecondition with the skill itself and its folder", results[0].Error)
	}
}

// 界面上合成一处的两个 Agent 各有这个组的组安装、成员各装在自己的目录里:UninstallGroupFrom 一次撤掉两条。
// 第二个 Agent 那一处在确认之后才出现旧链接时整次不做,第一个 Agent 的组安装和文件夹也都还在;同意删掉链接后两处一起撤。
func TestUninstallGroupFromCommitsTheScopesTogether(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	all := []string{"~/.d1", "~/.d2", "~/.d3"}
	a, err := lib.AddAgent(ctx, library.AgentInput{Name: "A", GlobalDirectories: all, ProjectDirectories: []string{".a/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.AddAgent(ctx, library.AgentInput{Name: "B", GlobalDirectories: []string{"~/.d2"}, ProjectDirectories: []string{".b/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	foo := fetchNamedSkill(t, lib, "foo")
	group, err := lib.CreateGroup(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, foo.ID); err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{library.GlobalScope(a.Code), library.GlobalScope(b.Code)}
	for _, scope := range scopes {
		if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.EditAgent(ctx, b.Code, library.AgentInput{Name: "B", GlobalDirectories: all, ProjectDirectories: []string{".b/skills"}}, library.EditAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	d1, d2, d3 := filepath.Join(home, ".d1"), filepath.Join(home, ".d2"), filepath.Join(home, ".d3")
	requireInstalledFolder(t, d1, "foo", "# foo\n")
	requireInstalledFolder(t, d2, "foo", "# foo\n")
	if err := os.MkdirAll(d3, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d3, "foo")
	if err := os.Symlink(filepath.Join(d2, "foo"), link); err != nil {
		t.Fatal(err)
	}

	err = lib.UninstallGroupFrom(ctx, group.ID, scopes, library.UninstallOptions{})
	requireErrorDetails(t, err, library.Conflict, library.ErrorDetails{Links: []string{link}})
	requireInstalledFolder(t, d1, "foo", "# foo\n")
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 2 {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want both kept", installs, err)
	}

	if err := lib.UninstallGroupFrom(ctx, group.ID, scopes, library.UninstallOptions{DeleteLinks: []string{link}}); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, d1, "foo")
	requireNoInstalledFolder(t, d2, "foo")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link after uninstalling = %v; want removed", err)
	}
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want none", installs, err)
	}
	requireLibraryError(t, lib.UninstallGroupFrom(ctx, group.ID, scopes, library.UninstallOptions{}), library.NotFound)
	if err := lib.UninstallGroupFrom(ctx, group.ID, nil, library.UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
}
