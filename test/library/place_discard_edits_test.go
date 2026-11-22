package library_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 删技能文件夹的操作在动磁盘之前核对:确认时没告诉用户、现在却被改过的文件夹,一个都不删,
// 用户同意丢掉这些修改(DiscardEdits)后再删。
type discardEditsFixture struct {
	lib     *library.Library
	skill   library.Skill
	other   library.Skill
	group   library.Group
	project library.RegisteredProject
	agent   library.Agent
	scope   library.Scope
	folder  string
	another string
}

func newDiscardEditsFixture(t *testing.T, scopeOf func(f *discardEditsFixture) library.Scope, viaGroup bool) *discardEditsFixture {
	t.Helper()
	ctx := t.Context()
	lib := openTestLibrary(t)
	f := &discardEditsFixture{lib: lib, skill: fetchNamedSkill(t, lib, "alpha"), other: fetchNamedSkill(t, lib, "beta")}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.project = project
	f.agent, err = lib.AddAgent(ctx, library.CustomAgent{Name: "Mine", GlobalDirectory: "~/.mine/skills", ProjectDirectory: ".mine/skills"})
	if err != nil {
		t.Fatal(err)
	}
	f.group, err = lib.CreateGroup(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{f.skill.ID, f.other.ID} {
		if _, err := lib.AddSkillToGroup(ctx, f.group.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	f.scope = scopeOf(f)
	if viaGroup {
		if _, err := lib.InstallGroup(ctx, f.group.ID, f.scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, id := range []library.SkillID{f.skill.ID, f.other.ID} {
			if err := lib.Install(ctx, id, f.scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	directory, err := lib.ScopeDirectory(ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	f.folder, f.another = filepath.Join(directory, f.skill.Name), filepath.Join(directory, f.other.Name)
	return f
}

func (f *discardEditsFixture) edit(t *testing.T) {
	t.Helper()
	writeFile(t, filepath.Join(f.folder, "notes.md"), []byte("my notes\n"), 0o644)
}

func (f *discardEditsFixture) requireEdited(t *testing.T) {
	t.Helper()
	if content, err := os.ReadFile(filepath.Join(f.folder, "notes.md")); err != nil || string(content) != "my notes\n" {
		t.Fatalf("edited notes.md = %q, %v; want the edit kept", content, err)
	}
}

// requireFolders 断言这两个技能的文件夹都还在(present)或都删了。
func (f *discardEditsFixture) requireFolders(t *testing.T, present bool) {
	t.Helper()
	for _, folder := range []string{f.folder, f.another} {
		if _, err := os.Lstat(folder); (err == nil) != present {
			t.Fatalf("%s: Lstat = %v; want present = %v", folder, err, present)
		}
	}
}

func TestDeletingFoldersRefusesEditsTheConfirmationDidNotMention(t *testing.T) {
	t.Parallel()
	project := func(f *discardEditsFixture) library.Scope {
		return library.ProjectScope(library.AgentClaudeCode, f.project.ID)
	}
	mine := func(f *discardEditsFixture) library.Scope { return library.GlobalScope(f.agent.Code) }
	cases := []struct {
		name     string
		scope    func(f *discardEditsFixture) library.Scope
		viaGroup bool
		run      func(ctx context.Context, f *discardEditsFixture, discard []string) error
	}{
		{"Uninstall", project, false, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.Uninstall(ctx, f.skill.ID, f.scope, library.UninstallOptions{DiscardEdits: discard})
		}},
		{"UninstallSkills", project, false, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			places := []library.SkillInScope{{SkillID: f.other.ID, Scope: f.scope}, {SkillID: f.skill.ID, Scope: f.scope}}
			return f.lib.UninstallSkills(ctx, places, library.UninstallOptions{DiscardEdits: discard})
		}},
		{"UninstallGroup", project, true, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.UninstallGroup(ctx, f.group.ID, f.scope, library.UninstallOptions{DiscardEdits: discard})
		}},
		{"RemoveSkill", project, false, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.RemoveSkill(ctx, f.skill.ID, library.RemoveSkillOptions{DiscardEdits: discard})
		}},
		{"RemoveSkills", project, false, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.RemoveSkills(ctx, []library.SkillID{f.other.ID, f.skill.ID}, library.RemoveSkillOptions{DiscardEdits: discard})
		}},
		{"RemoveSkillFromGroup", project, true, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.RemoveSkillFromGroup(ctx, f.group.ID, f.skill.ID, library.RemoveFromGroupOptions{IfNoOtherInstall: library.UninstallMember, DiscardEdits: discard})
		}},
		{"RemoveSkillsFromGroup", project, true, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.RemoveSkillsFromGroup(ctx, f.group.ID, []library.SkillID{f.other.ID, f.skill.ID}, library.RemoveFromGroupOptions{IfNoOtherInstall: library.UninstallMember, DiscardEdits: discard})
		}},
		{"DeleteGroup", project, true, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.DeleteGroup(ctx, f.group.ID, library.DeleteGroupOptions{IfNoOtherInstall: library.UninstallMember, DiscardEdits: discard})
		}},
		{"UnregisterProject", project, false, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			return f.lib.UnregisterProject(ctx, f.project.ID, library.UnregisterProjectOptions{DiscardEdits: discard})
		}},
		{"DeleteAgent", mine, false, func(ctx context.Context, f *discardEditsFixture, discard []string) error {
			_, err := f.lib.DeleteAgent(ctx, f.agent.Code, library.DeleteAgentOptions{DiscardEdits: discard})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newDiscardEditsFixture(t, tc.scope, tc.viaGroup)
			before := listSkillScopes(t, f.lib)
			f.edit(t)

			// 确认时没说这一处被改过:整次不做,文件夹、修改与安装记录都还在,错误里写出是哪个文件夹。
			err := tc.run(ctx, f, nil)
			requireErrorDetails(t, err, library.Conflict, library.ErrorDetails{Paths: []string{f.folder}})
			f.requireEdited(t)
			if _, err := os.Stat(f.another); err != nil {
				t.Fatalf("other folder after the refused call: %v", err)
			}
			if after := listSkillScopes(t, f.lib); !reflect.DeepEqual(withoutCreatedAt(after), withoutCreatedAt(before)) {
				t.Fatalf("skill scopes after the refused call = %#v; want unchanged %#v", after, before)
			}

			// 用户同意丢掉这一处的修改后照常删。
			if err := tc.run(ctx, f, []string{f.folder}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(f.folder); !os.IsNotExist(err) {
				t.Fatalf("edited folder after confirming = %v; want removed", err)
			}
		})
	}
}

func TestDeletingFoldersRemovesFoldersConfirmedAsEditedButNoLongerEdited(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newDiscardEditsFixture(t, func(*discardEditsFixture) library.Scope { return library.GlobalScope(library.AgentClaudeCode) }, false)
	if err := f.lib.Uninstall(ctx, f.skill.ID, f.scope, library.UninstallOptions{DiscardEdits: []string{f.folder}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.folder); !os.IsNotExist(err) {
		t.Fatalf("folder = %v; want removed", err)
	}
}

func TestDeletingFoldersDoesNotCheckFoldersItKeeps(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newDiscardEditsFixture(t, func(*discardEditsFixture) library.Scope { return library.GlobalScope(library.AgentClaudeCode) }, true)
	f.edit(t)
	if err := f.lib.RemoveSkill(ctx, f.skill.ID, library.RemoveSkillOptions{Folders: library.Keep}); err != nil {
		t.Fatal(err)
	}
	if err := f.lib.DeleteGroup(ctx, f.group.ID, library.DeleteGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	f.requireEdited(t)
}

func TestDeletingALinkDoesNotCountEditsInTheLinkedFolder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newLinkedFixture(t)
	writeFile(t, filepath.Join(f.codexFolder, "SKILL.md"), []byte(skillDocument("tdd", "edited in codex")), 0o644)
	if err := f.lib.Uninstall(ctx, f.skill.ID, f.claude, library.UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, filepath.Dir(f.codexFolder), "tdd", "edited in codex")
}

func TestDeletingFoldersKeepsEverythingWhenTheCheckCannotReadAFolder(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root 不受文件权限限制")
	}
	ctx := t.Context()
	f := newDiscardEditsFixture(t, func(*discardEditsFixture) library.Scope { return library.GlobalScope(library.AgentClaudeCode) }, false)
	unreadable := filepath.Join(f.folder, "SKILL.md")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })
	err := f.lib.RemoveSkills(ctx, []library.SkillID{f.skill.ID, f.other.ID}, library.RemoveSkillOptions{})
	if err == nil || library.IsCode(err, library.Conflict) {
		t.Fatalf("RemoveSkills with an unreadable folder = %v; want the read failure", err)
	}
	f.requireFolders(t, true)
	if skills, err := f.lib.ListSkills(ctx); err != nil || len(skills) != 2 {
		t.Fatalf("ListSkills() = %d skills, %v; want both kept", len(skills), err)
	}
}
