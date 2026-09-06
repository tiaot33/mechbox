package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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
			lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lib.Close() })
			group, err := lib.CreateGroup(ctx, "batch")
			if err != nil {
				t.Fatal(err)
			}
			scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
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
			firstPath := filepath.Join(scope.Path, "first", "SKILL.md")
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
				content, err = os.ReadFile(filepath.Join(scope.Path, "second", "SKILL.md"))
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
				content, err := os.ReadFile(filepath.Join(scope.Path, skill.Name, "SKILL.md"))
				if !deactivate && skill.Name == "second" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("partial projection remains: %v", err)
					}
				} else if err != nil || string(content) != skill.Name {
					t.Fatalf("projection %s changed: %q, %v", skill.Name, content, err)
				}
			}
			entries, err := os.ReadDir(scope.Path)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name()[0] == '.' {
					t.Fatalf("temporary projection remains: %s", entry.Name())
				}
			}
		})
	}
}

func TestGroupConflictPoliciesHandleDanglingSymlinks(t *testing.T) {
	ctx := t.Context()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
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
	scope := library.Scope{Agent: library.AgentCodex, Path: t.TempDir()}
	target := filepath.Join(scope.Path, skill.Name)
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
