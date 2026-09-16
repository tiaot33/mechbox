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

	"github.com/tiaot33/skills-tui/library"
)

// 在观察到指定成员的磁盘改动后同步取消,不依赖调度时序或内部故障注入接口。
type cancelAfterProjection struct {
	context.Context
	cancel  context.CancelFunc
	changed func() bool
}

func (c cancelAfterProjection) Err() error {
	if c.changed() {
		c.cancel()
	}
	return c.Context.Err()
}

func TestGroupMidBatchCancellationRestoresProjectionsAndOrigins(t *testing.T) {
	for _, deactivate := range []bool{false, true} {
		name := "activate"
		if deactivate {
			name = "deactivate"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			group, err := lib.CreateGroup(ctx, "batch")
			if err != nil {
				t.Fatal(err)
			}
			scope := library.GlobalScope{Agent: library.AgentCodex}
			directory, err := lib.Resolve(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			var skills []library.Skill
			for _, name := range []string{"first", "second", "independent", "shared"} {
				source := filepath.Join(t.TempDir(), name)
				writeFile(t, filepath.Join(source, "SKILL.md"), []byte(name), 0o644)
				skill, err := lib.Install(ctx, library.LocalSource{Path: source})
				if err != nil {
					t.Fatal(err)
				}
				skills = append(skills, skill)
				if name != "independent" {
					if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, skill := range skills[2:] {
				if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			other, err := lib.CreateGroup(ctx, "other")
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.AddSkillToGroup(ctx, other.ID, skills[3].ID); err != nil {
				t.Fatal(err)
			}
			if err := lib.ActivateGroup(ctx, other.ID, scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
			firstPath := filepath.Join(directory, "first", "SKILL.md")
			if deactivate {
				if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				// 首个成员覆盖现有内容,第二个成员原本没有投影。
				if err := lib.Activate(ctx, skills[0].ID, scope, library.ActivateOptions{}); err != nil {
					t.Fatal(err)
				}
				writeFile(t, firstPath, []byte("original local content"), 0o644)
			}
			before, err := lib.ListActivations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(firstPath)
			if err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(ctx)
			defer cancel()
			fault := cancelAfterProjection{Context: base, cancel: cancel, changed: func() bool {
				content, err := os.ReadFile(firstPath)
				if deactivate {
					return errors.Is(err, os.ErrNotExist)
				}
				// 已覆盖第一个成员并新增第二个成员,让回滚同时验证恢复与移除。
				content, err = os.ReadFile(filepath.Join(directory, "second", "SKILL.md"))
				return err == nil && string(content) == "second"
			}}
			if deactivate {
				err = lib.DeactivateGroup(fault, group.ID, scope)
			} else {
				err = lib.ActivateGroup(fault, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("mid-batch error = %v, want cancellation", err)
			}
			after, err := lib.ListActivations(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("origins changed: before=%#v after=%#v err=%v", before, after, err)
			}
			content, err := os.ReadFile(firstPath)
			if err != nil || string(content) != string(original) {
				t.Fatalf("first projection not restored: %q, %v", content, err)
			}
			for _, skill := range skills[1:] {
				content, err := os.ReadFile(filepath.Join(directory, skill.Name, "SKILL.md"))
				if !deactivate && skill.Name == "second" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("partial projection remains: %v", err)
					}
				} else if err != nil || string(content) != skill.Name {
					t.Fatalf("projection %s changed: %q, %v", skill.Name, content, err)
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
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("example"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
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
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Fail}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("Fail = %v, want AlreadyExists", err)
	}
	if got, err := os.Readlink(target); err != nil || got != missing {
		t.Fatalf("conflict changed: %q, %v", got, err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(content) != "example" {
		t.Fatalf("Overwrite = %q, %v", content, err)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(missing, target); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeactivateGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deactivation left dangling projection: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("activations = %#v, %v", activations, err)
	}
}

func TestGroupRollbackPreservesConcurrentActivation(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	group, err := lib.CreateGroup(ctx, "batch")
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var first library.Skill
	for _, name := range []string{"first", "second"} {
		source := filepath.Join(t.TempDir(), name)
		writeFile(t, filepath.Join(source, "SKILL.md"), []byte(name), 0o644)
		skill, err := lib.Install(ctx, library.LocalSource{Path: source})
		if err != nil {
			t.Fatal(err)
		}
		if first.ID == 0 {
			first = skill
		}
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(directory, first.Name, "SKILL.md")
	writeFile(t, target, []byte("original"), 0o644)
	projected, release := make(chan struct{}), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	defer resume()
	pause := sync.OnceFunc(func() { close(projected); <-release })
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	fault := cancelAfterProjection{Context: base, cancel: cancel, changed: func() bool {
		content, err := os.ReadFile(target)
		if err != nil || string(content) != "first" {
			return false
		}
		pause()
		return true
	}}
	groupDone := make(chan error, 1)
	go func() {
		groupDone <- lib.ActivateGroup(fault, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	}()
	select {
	case <-projected:
	case <-time.After(5 * time.Second):
		t.Fatal("group never projected first member")
	}
	individualDone := make(chan error, 1)
	go func() {
		individualDone <- lib.Activate(ctx, first.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	}()
	// 旧实现允许独立激活在回滚前提交;串行实现则等分组回滚后再完成。
	var individualErr error
	finished := false
	select {
	case individualErr = <-individualDone:
		finished = true
	case <-time.After(200 * time.Millisecond):
	}
	resume()
	if err := <-groupDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("group error = %v", err)
	}
	if !finished {
		individualErr = <-individualDone
	}
	if individualErr != nil {
		t.Fatal(individualErr)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "first" {
		t.Fatalf("successful independent projection lost: %q, %v", content, err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: first.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("activations = %#v, %v; want %#v", activations, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, "second")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial group projection remains: %v", err)
	}
}
