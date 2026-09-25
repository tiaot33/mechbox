package library_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// Open 只补缺失的 Agent 行、不覆盖已有布局;已按身份记录的投影记录在重开后按现行规则派生目录,
// 并可继续按身份激活。
func TestOpenRepairsMissingAgentsAndAcceptsExistingRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("example\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope(library.AgentClaudeCode),
		library.GlobalScope(library.AgentCodex),
		library.ProjectScope(library.AgentCodex, project.ID),
	}
	for _, scope := range scopes {
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	// 只用 SQL 构造缺 Agent 行且另一行布局被改过的输入文件;结果仍由公开门面观察。
	editLibraryFixture(t, path, `
		DELETE FROM agents WHERE code = 'codex';
		UPDATE agents SET name = 'Custom Claude', global_path = '.custom/global', project_path = '.custom/project' WHERE code = 'claude-code';
	`)
	// 目录按现行布局派生:claude-code 用表里保留的自定义规则,补种子的 codex 用种子规则。
	want := []library.Projection{
		{SkillID: skill.ID, Scope: scopes[0], Directory: filepath.Join(home, ".custom", "global"), Direct: true},
		{SkillID: skill.ID, Scope: scopes[1], Directory: filepath.Join(home, ".agents", "skills"), Direct: true},
		{SkillID: skill.ID, Scope: scopes[2], Directory: filepath.Join(project.Path, ".agents", "skills"), Direct: true},
	}
	for range 2 {
		reopened, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		got, err := reopened.ListProjections(ctx)
		if err != nil || !reflect.DeepEqual(withoutActivatedAt(got), want) {
			t.Fatalf("ListProjections after Open = %#v, %v; want %#v", got, err, want)
		}
		for _, projection := range want {
			if err := reopened.Activate(ctx, skill.ID, projection.Scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsMissingScopeIdentityConstraint(t *testing.T) {
	for _, index := range []string{"projections_global_scope", "projections_project_scope", "group_activations_global_scope", "group_activations_project_scope"} {
		t.Run(index, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			lib, err := library.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.Close(); err != nil {
				t.Fatal(err)
			}
			editLibraryFixture(t, path, `DROP INDEX `+index+`; DELETE FROM agents WHERE code = 'codex'`)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if invalid, err := library.Open(path); err == nil {
				_ = invalid.Close()
				t.Fatal("Open accepted a library without its Scope identity constraint")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected Open changed the library file: %v", err)
			}
		})
	}
}

// v8 库的投影表:组只挂在投影上(activation_groups),没有组激活记录。
const v8ProjectionTables = `
DROP TABLE projections;
DROP TABLE group_activations;
CREATE TABLE activations (
    id INTEGER PRIMARY KEY,
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    agent TEXT NOT NULL REFERENCES agents(code),
    project_id INTEGER REFERENCES projects(id),
    individual INTEGER NOT NULL DEFAULT 0,
    activated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX activations_global_scope ON activations(skill_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX activations_project_scope ON activations(skill_id, agent, project_id) WHERE project_id IS NOT NULL;
CREATE TABLE activation_groups (
    activation_id INTEGER NOT NULL REFERENCES activations(id) ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES groups(id),
    PRIMARY KEY(activation_id, group_id)
);
PRAGMA user_version = 8;
`

// v8 → 当前:组激活按去重的 Group × Scope 回填(时间未知);投影保留 ID、直接激活与建立时间,覆盖它的组
// 由成员关系推出。已移出组却仍挂着该组的投影(#85 的卡住状态)转为直接激活,可以单独停用。
func TestOpenMigratesV8GroupsOnProjectionsToGroupActivations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	var skills []library.Skill
	for _, name := range []string{"removed", "member", "direct"} {
		skills = append(skills, installNamedSkill(t, lib, name))
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills[1:] {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	const activatedAt = "2026-01-02T03:04:05Z"
	editLibraryFixture(t, path, v8ProjectionTables+fmt.Sprintf(`
		INSERT INTO activations(id, skill_id, agent, project_id, individual, activated_at) VALUES
			(10, %[1]d, 'codex', NULL, 0, '%[5]s'),
			(11, %[2]d, 'codex', NULL, 0, '%[5]s'),
			(12, %[3]d, 'codex', %[4]d, 1, '%[5]s'),
			(13, %[2]d, 'codex', %[4]d, 0, '%[5]s');
		INSERT INTO activation_groups(activation_id, group_id) VALUES (10, %[6]d), (11, %[6]d), (12, %[6]d), (13, %[6]d);
	`, skills[0].ID, skills[1].ID, skills[2].ID, project.ID, activatedAt, group.ID))

	migrated, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	if got := readLibraryScalar(t, path, `PRAGMA user_version`); got != "9" {
		t.Fatalf("user_version = %s, want 9", got)
	}
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	activations, err := migrated.ListGroupActivations(ctx)
	want := []library.GroupActivation{{GroupID: group.ID, Scope: global}, {GroupID: group.ID, Scope: projectScope}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListGroupActivations() = %#v, %v; want %#v", activations, err, want)
	}
	globalDir, err := migrated.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	projectDir, err := migrated.Resolve(ctx, projectScope)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, activatedAt)
	if err != nil {
		t.Fatal(err)
	}
	via := []library.GroupID{group.ID}
	wantProjections := []library.Projection{
		{SkillID: skills[0].ID, Scope: global, Directory: globalDir, Direct: true, ActivatedAt: at},
		{SkillID: skills[1].ID, Scope: global, Directory: globalDir, Groups: via, ActivatedAt: at},
		{SkillID: skills[1].ID, Scope: projectScope, Directory: projectDir, Groups: via, ActivatedAt: at},
		{SkillID: skills[2].ID, Scope: projectScope, Directory: projectDir, Direct: true, Groups: via, ActivatedAt: at},
	}
	projections, err := migrated.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(projections, wantProjections) {
		t.Fatalf("ListProjections() = %#v, %v; want %#v", projections, err, wantProjections)
	}
	if err := migrated.Deactivate(ctx, skills[0].ID, global); err != nil {
		t.Fatalf("Deactivate stuck projection after migration = %v, want success", err)
	}
	// 回填的组激活照常覆盖新成员。
	if _, err := migrated.AddSkillToGroup(ctx, group.ID, skills[0].ID, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	requireProjection(t, globalDir, "removed", "# removed\n")
}
