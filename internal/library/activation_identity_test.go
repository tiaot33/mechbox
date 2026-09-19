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

// 用项目 Scope 激活:文件夹落在该 Registered project 根下 Resolve 出的目录;Deactivate 后
// 目录与记录一起消失,Library 里的 skill 仍在;再次 Deactivate 得到 NotFound。
func TestActivateAndDeactivateProjectScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	scope := library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: project.ID}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
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
		t.Fatalf("project projection missing: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Activation{{
		SkillID:    skill.ID,
		Scope:      scope,
		Directory:  directory,
		Individual: true,
	}}
	if !reflect.DeepEqual(withoutActivatedAt(activations), want) {
		t.Fatalf("ListActivations() = %#v, want %#v", activations, want)
	}

	if err := lib.Deactivate(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deactivated directory still exists: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("Deactivate removed library skill: %v", err)
	}
	if activations, err := lib.ListActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after deactivate = %#v, %v", activations, err)
	}
	if err := lib.Deactivate(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Deactivate() inactive skill error = %v, want NotFound", err)
	}
}

// 整组激活在身份路径上仍是原子的:某成员目标被外来文件夹占用时整组拒绝,不留半组投影。
func TestActivateGroupOnIdentityScopeIsAtomic(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := installExampleSkill(t, lib, "first")
	second := installExampleSkill(t, lib, "second")
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, second.Name, "manual.txt"), []byte("manual"), 0o644)

	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("ActivateGroup() conflict error = %v, want AlreadyExists", err)
	}
	if _, err := os.Stat(filepath.Join(directory, first.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first member was partially activated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	if activations, err := lib.ListActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() = %#v, %v; want none", activations, err)
	}
}

// 同一 skill 可同时占用两个 Agent 的全局 Scope,以及同一 Agent 的全局与项目 Scope:各是一条 Activation。
func TestIdentityScopesOfOneSkillAreIndependentActivations(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope{Agent: library.AgentClaudeCode},
		library.GlobalScope{Agent: library.AgentCodex},
		library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: project.ID},
	}
	for _, scope := range scopes {
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatalf("Activate(%#v) = %v", scope, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != len(scopes) {
		t.Fatalf("ListActivations() = %#v, %v; want %d activations", activations, err, len(scopes))
	}
	for _, scope := range scopes {
		directory, err := lib.Resolve(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("projection for %#v missing: %v", scope, err)
		}
	}
	// 撤销其中一条不影响其余两条。
	if err := lib.Deactivate(ctx, skill.ID, scopes[0]); err != nil {
		t.Fatal(err)
	}
	activations, err = lib.ListActivations(ctx)
	if err != nil || len(activations) != 2 {
		t.Fatalf("ListActivations() after one Deactivate = %#v, %v; want 2", activations, err)
	}
	for _, activation := range activations {
		if activation.Scope == scopes[0] {
			t.Fatalf("deactivated scope still listed: %#v", activation)
		}
	}
}

// 两个不同身份的 Scope 解析到同一目录(把 $HOME 本身登记为项目)时,第二次激活不能悄悄改写
// 第一条 Activation 的身份:得到 AlreadyExists,原记录与磁盘保持原样。
func TestActivateDoesNotRewriteAnotherScopeSharingTheDirectory(t *testing.T) {
	ctx := t.Context()
	// RegisterProject 会解析符号链接(macOS 的 /var → /private/var),HOME 先取规范路径以便两边一致。
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope{Agent: library.AgentCodex}
	projectScope := library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID}
	directory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if projectDirectory, err := lib.Resolve(ctx, projectScope); err != nil || projectDirectory != directory {
		t.Fatalf("precondition: scopes should share a directory, got %q vs %q (%v)", projectDirectory, directory, err)
	}

	if err := lib.Activate(ctx, skill.ID, global, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []library.ActivateOptions{{}, {OnConflict: library.Overwrite}} {
		err := lib.Activate(ctx, skill.ID, projectScope, opts)
		requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: directory})
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: skill.ID, Scope: global, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(activations), want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("global projection lost: %v", err)
	}
}

// 两个身份争同一实际目录时,并发 Activate 也不能绕过占用保护:最多一条记录,磁盘副本仍在。
func TestActivateSerializesSharedDirectoryClaims(t *testing.T) {
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
	skill := installExampleSkill(t, lib, "example-skill")
	scopes := []library.Scope{
		library.GlobalScope{Agent: library.AgentCodex},
		library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID},
	}
	start := make(chan struct{})
	results := make([]error, len(scopes))
	var calls sync.WaitGroup
	for i, scope := range scopes {
		calls.Go(func() {
			<-start
			results[i] = lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{})
		})
	}
	close(start)
	calls.Wait()
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(activations) != 1 {
		t.Fatalf("ListActivations() = %#v, activate errors=%v; want exactly one claim", activations, results)
	}
	directory, err := lib.Resolve(ctx, activations[0].Scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("surviving projection missing: %v", err)
	}
	var accepted, rejected int
	for _, err := range results {
		switch {
		case err == nil:
			accepted++
		case library.IsCode(err, library.FailedPrecondition):
			rejected++
		default:
			t.Fatalf("Activate = %v, want nil or FailedPrecondition", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("Activate results = %v, want one success and one FailedPrecondition", results)
	}
}

// 同名不同 Source 的 A 已投影到某 Scope 时,激活 B 不能接管那个文件夹:两种冲突策略都得到
// FailedPrecondition 并指认占用者;A 的记录与内容原样保留,不会出现两条记录指向同一文件夹。
func TestActivateRejectsNameClaimedByAnotherSkill(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := installSkillWithBody(t, lib, "commit", "# A\n")
	second := installSkillWithBody(t, lib, "commit", "# B\n")
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(directory, "commit")
	for _, opts := range []library.ActivateOptions{{}, {OnConflict: library.Overwrite}} {
		err := lib.Activate(ctx, second.ID, scope, opts)
		requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID})
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: first.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(activations), want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}
	requireProjection(t, directory, "commit", "# A\n")

	// 换用 B 的正确路径:先撤 A,再激活 B。
	if err := lib.Deactivate(ctx, first.ID, scope); err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, second.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	requireProjection(t, directory, "commit", "# B\n")
	activations, err = lib.ListActivations(ctx)
	want = []library.Activation{{SkillID: second.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(activations), want) {
		t.Fatalf("ListActivations() after switch = %#v, %v; want %#v", activations, err, want)
	}
}

// 占用者经分组投影(无独立激活)时同样受保护;含同名成员的分组向该 Scope 激活时整组被拒,
// 其他成员不落盘、不留记录。
func TestActivateGroupRejectsNameClaimedByAnotherSkill(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := installSkillWithBody(t, lib, "commit", "# A\n")
	second := installSkillWithBody(t, lib, "commit", "# B\n")
	bystander := installSkillWithBody(t, lib, "bystander", "# bystander\n")
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	firstGroup, err := lib.CreateGroup(ctx, "holders")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, firstGroup.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, firstGroup.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(directory, "commit")
	err = lib.Activate(ctx, second.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID})

	secondGroup, err := lib.CreateGroup(ctx, "challengers")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{bystander, second} {
		if err := lib.AddSkillToGroup(ctx, secondGroup.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	err = lib.ActivateGroup(ctx, secondGroup.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID})
	if _, err := os.Stat(filepath.Join(directory, "bystander")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected group projected another member: %v", err)
	}
	requireProjection(t, directory, "commit", "# A\n")
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: first.ID, Scope: scope, Directory: directory, ViaGroups: []library.GroupID{firstGroup.ID}}}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(activations), want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}
}

// 占用以实际目录为准:A 在全局 Scope,把 $HOME 登记为项目后向项目 Scope 激活同名的 B 同样被拒;
// 而 A、B 投影到不同目录的 Scope 互不干扰。即使 A 的文件夹已被手删,只要记录在,B 仍被拒。
func TestSameNameSkillsCollideOnlyInTheSameDirectory(t *testing.T) {
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	first := installSkillWithBody(t, lib, "commit", "# A\n")
	second := installSkillWithBody(t, lib, "commit", "# B\n")
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope{Agent: library.AgentCodex}
	sameDirectory := library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID}
	otherAgent := library.GlobalScope{Agent: library.AgentClaudeCode}
	directory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, first.ID, global, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}

	err = lib.Activate(ctx, second.ID, sameDirectory, library.ActivateOptions{OnConflict: library.Overwrite})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(directory, "commit"), Name: "commit", SkillID: first.ID})

	if err := lib.Activate(ctx, second.ID, otherAgent, library.ActivateOptions{}); err != nil {
		t.Fatalf("Activate(B) into another agent's directory = %v, want success", err)
	}
	otherDirectory, err := lib.Resolve(ctx, otherAgent)
	if err != nil {
		t.Fatal(err)
	}
	requireProjection(t, directory, "commit", "# A\n")
	requireProjection(t, otherDirectory, "commit", "# B\n")
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{
		{SkillID: first.ID, Scope: global, Directory: directory, Individual: true},
		{SkillID: second.ID, Scope: otherAgent, Directory: otherDirectory, Individual: true},
	}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(activations), want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}

	if err := os.RemoveAll(filepath.Join(directory, "commit")); err != nil {
		t.Fatal(err)
	}
	err = lib.Activate(ctx, second.ID, global, library.ActivateOptions{})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(directory, "commit"), Name: "commit", SkillID: first.ID})
}

// 身份 Scope 里的错误指认在激活时同样得到 NotFound,不会写到任何目录。
func TestActivateUnknownAgentOrProjectIsNotFound(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")

	for name, scope := range map[string]library.Scope{
		"unknown agent":        library.GlobalScope{Agent: "cursor"},
		"unregistered project": library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: 42},
	} {
		t.Run(name, func(t *testing.T) {
			if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Activate(%#v) = %v, want NotFound", scope, err)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed Activate wrote into HOME: %v, %v", entries, err)
	}
}
