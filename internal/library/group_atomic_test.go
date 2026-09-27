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

	"github.com/tiaot33/skills-tui/internal/library"
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

func TestGroupMidBatchCancellationRestoresInstallationsAndOrigins(t *testing.T) {
	for _, uninstall := range []bool{false, true} {
		name := "install"
		if uninstall {
			name = "uninstall"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			group, err := lib.CreateGroup(ctx, "batch")
			if err != nil {
				t.Fatal(err)
			}
			scope := library.GlobalScope(library.AgentCodex)
			directory, err := lib.Resolve(ctx, scope)
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
				// 首个成员覆盖未记录的文件夹,第二个成员原本没有安装项;shared 已有安装项,不写盘。
				writeFile(t, firstPath, []byte("original local content"), 0o644)
			}
			before, err := lib.ListInstallations(ctx)
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
				err = lib.UninstallGroup(fault, group.ID, scope)
			} else {
				_, err = lib.InstallGroup(fault, group.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("mid-batch error = %v, want cancellation", err)
			}
			after, err := lib.ListInstallations(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("installations changed: before=%#v after=%#v err=%v", before, after, err)
			}
			installsAfter, err := lib.ListGroupInstalls(ctx)
			if err != nil || !reflect.DeepEqual(installsBefore, installsAfter) {
				t.Fatalf("group installs changed: before=%#v after=%#v err=%v", installsBefore, installsAfter, err)
			}
			content, err := os.ReadFile(firstPath)
			if err != nil || string(content) != string(original) {
				t.Fatalf("first installation not restored: %q, %v", content, err)
			}
			for _, skill := range skills[1:] {
				content, err := os.ReadFile(filepath.Join(directory, skill.Name, "SKILL.md"))
				if !uninstall && skill.Name == "second" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("partial installation remains: %v", err)
					}
				} else if err != nil || string(content) != skillDocument(skill.Name, skill.Name) {
					t.Fatalf("installation %s changed: %q, %v", skill.Name, content, err)
				}
			}
			requireNoStagedEntries(t, directory)
		})
	}
}

func TestGroupConflictPoliciesHandleDanglingSymlinks(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
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
	directory, err := lib.Resolve(ctx, scope)
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
	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{OnConflict: library.Fail})
	requireMemberResults(t, results, err, []library.GroupMemberResult{skippedMember(skill.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: target})})
	if got, err := os.Readlink(target); err != nil || got != missing {
		t.Fatalf("conflict changed: %q, %v", got, err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{OnConflict: library.Overwrite}); err != nil {
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
	if err := lib.UninstallGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left dangling installation: %v", err)
	}
	installations, err := lib.ListInstallations(ctx)
	if err != nil || len(installations) != 0 {
		t.Fatalf("installations = %#v, %v", installations, err)
	}
}

func TestGroupRollbackPreservesConcurrentInstall(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	group, err := lib.CreateGroup(ctx, "batch")
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
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
		_, err := lib.InstallGroup(fault, group.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
		groupDone <- err
	}()
	select {
	case <-installed:
	case <-time.After(5 * time.Second):
		t.Fatal("group never installed first member")
	}
	directDone := make(chan error, 1)
	go func() {
		directDone <- lib.Install(ctx, first.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
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
		t.Fatalf("successful direct install installation lost: %q, %v", content, err)
	}
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("installations = %#v, %v; want %#v", installations, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, "second")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial group installation remains: %v", err)
	}
}
