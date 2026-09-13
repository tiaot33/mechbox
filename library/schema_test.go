package library_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestOpenMigratesLegacyLibraryAndPreservesActivationOrigins(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "library.db")
			schema, err := os.ReadFile(fmt.Sprintf("testdata/v%d.sql", version))
			if err != nil {
				t.Fatal(err)
			}
			editLibraryFixture(t, path, string(schema))
			editLibraryFixture(t, path, fmt.Sprintf(`PRAGMA application_id = 1397443650; PRAGMA user_version = %d`, version))
			editLibraryFixture(t, path, `
				INSERT INTO sources(id, type, locator) VALUES (1, 'local', '{"path":"/legacy/source"}');
				INSERT INTO skills(id, source_id, name, current_version_id) VALUES (1, 1, 'example', 1);
				INSERT INTO skill_versions(id, skill_id) VALUES (1, 1);
				INSERT INTO blobs(fingerprint, content) VALUES ('fixture', X'6578616D706C650A');
				INSERT INTO version_files(version_id, path, fingerprint, mode) VALUES (1, 'SKILL.md', 'fixture', 'regular');
				INSERT INTO groups(id, name) VALUES (1, 'first'), (2, 'second');
				INSERT INTO skill_groups(skill_id, group_id) VALUES (1, 1), (1, 2);
			`)
			if version >= 2 {
				editLibraryFixture(t, path, `UPDATE skill_versions SET git_tree_sha = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE id = 1`)
			}
			if version == 3 {
				// 一行已有自定义规则,另一行缺失但被 Activation 引用,迁移须同时处理。
				editLibraryFixture(t, path, `INSERT INTO agents(code, global_path, project_path) VALUES ('claude-code', '.custom/global', '.custom/project')`)
			}
			projectPath := t.TempDir()
			editLibraryFixture(t, path, `INSERT INTO projects(id, path) VALUES (1, ?)`, projectPath)
			// 同一 Agent 的多个旧路径不能在 expand 阶段被合并为一条全局身份。
			want := []library.Activation{
				{SkillID: 1, Scope: library.Scope{Agent: library.AgentCodex, Path: filepath.Join(projectPath, ".codex", "skills")}, Individual: true, ViaGroups: []library.GroupID{1, 2}},
				{SkillID: 1, Scope: library.Scope{Agent: library.AgentCodex, Path: filepath.Join(projectPath, "custom")}, ViaGroups: []library.GroupID{2}},
			}
			for _, activation := range want {
				editLibraryFixture(t, path, `INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (1, 'codex', ?, ?)`, activation.Scope.Path, activation.Individual)
				for _, groupID := range activation.ViaGroups {
					if version == 3 {
						editLibraryFixture(t, path, `
							INSERT INTO activation_groups(activation_id, skill_id, agent, scope_path, group_id)
							SELECT id, skill_id, agent, scope_path, ? FROM activations WHERE scope_path = ?
						`, groupID, activation.Scope.Path)
					} else {
						editLibraryFixture(t, path, `INSERT INTO activation_groups(skill_id, agent, scope_path, group_id) VALUES (1, 'codex', ?, ?)`, activation.Scope.Path, groupID)
					}
				}
				writeFile(t, filepath.Join(activation.Scope.Path, "example", "SKILL.md"), []byte("example\n"), 0o644)
			}
			lib, err := library.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lib.Close() })
			got, err := lib.ListActivations(ctx)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("migrated activations = %#v, %v; want %#v", got, err, want)
			}
			projects, err := lib.ListRegisteredProjects(ctx)
			if err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{{ID: 1, Path: projectPath}}) {
				t.Fatalf("migrated projects = %#v, %v", projects, err)
			}
			history, err := lib.GetVersion(ctx, 1)
			if err != nil || len(history.Files) != 1 || string(history.Files[0].Content) != "example\n" {
				t.Fatalf("migrated version = %#v, %v", history, err)
			}
			wantSHA := ""
			if version >= 2 {
				wantSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			if history.GitTreeSHA != wantSHA {
				t.Fatalf("migrated GitTreeSHA = %q, want %q", history.GitTreeSHA, wantSHA)
			}
			if err := lib.ActivateGroup(ctx, 2, want[1].Scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := lib.DeactivateGroup(ctx, 2, want[1].Scope); err != nil {
				t.Fatal(err)
			}
			if err := lib.DeleteGroup(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if err := lib.DeactivateGroup(ctx, 2, want[0].Scope); err != nil {
				t.Fatal(err)
			}
			if err := lib.Close(); err != nil {
				t.Fatal(err)
			}
			// 所有旧版升级后都应能接纳已有名称,重复 Open 不要求名称与种子相同。
			editLibraryFixture(t, path, `UPDATE agents SET name = 'Custom Claude' WHERE code = 'claude-code'`)
			reopened, err := library.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			want[0].ViaGroups = nil
			got, err = reopened.ListActivations(ctx)
			if err != nil || !reflect.DeepEqual(got, want[:1]) {
				t.Fatalf("reopened activations = %#v, %v; want %#v", got, err, want[:1])
			}
			if err := reopened.Deactivate(ctx, 1, want[0].Scope); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenRepairsMissingAgentsAndAcceptsExistingRules(t *testing.T) {
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
	root := t.TempDir()
	project, err := lib.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Activation{
		{SkillID: skill.ID, Scope: library.Scope{Agent: library.AgentClaudeCode, Path: filepath.Join(root, "claude")}, Individual: true},
		{SkillID: skill.ID, Scope: library.Scope{Agent: library.AgentCodex, Path: filepath.Join(root, "global")}, Individual: true},
		{SkillID: skill.ID, Scope: library.Scope{Agent: library.AgentCodex, Path: filepath.Join(root, "project")}, Individual: true},
	}
	for _, activation := range want {
		if err := lib.Activate(ctx, skill.ID, activation.Scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	// 只用 SQL 构造含全局/项目身份且缺 Agent 行的输入文件;结果仍由公开门面观察。
	editLibraryFixture(t, path, `
		DELETE FROM agents WHERE code = 'codex';
		UPDATE agents SET name = 'Custom Claude', global_path = '.custom/global', project_path = '.custom/project' WHERE code = 'claude-code';
	`)
	editLibraryFixture(t, path, `UPDATE activations SET scope_kind = 'global' WHERE scope_path <> ?`, want[2].Scope.Path)
	editLibraryFixture(t, path, `UPDATE activations SET scope_kind = 'project', project_id = ? WHERE scope_path = ?`, project.ID, want[2].Scope.Path)
	for range 2 {
		reopened, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		got, err := reopened.ListActivations(ctx)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("ListActivations after Open = %#v, %v; want %#v", got, err, want)
		}
		for _, activation := range want {
			if err := reopened.Activate(ctx, skill.ID, activation.Scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsMissingScopeIdentityConstraint(t *testing.T) {
	for _, index := range []string{"activations_global_scope", "activations_project_scope"} {
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

func TestOpenFailedMigrationCanRetryWithoutLosingLegacyData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	schema, err := os.ReadFile("testdata/v2.sql")
	if err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, string(schema))
	editLibraryFixture(t, path, `
		PRAGMA application_id = 1397443650;
		PRAGMA user_version = 2;
		INSERT INTO sources(id, type, locator) VALUES (1, 'local', '{"path":"/legacy/source"}');
		INSERT INTO skills(id, source_id, name) VALUES (1, 1, 'example');
		INSERT INTO groups(id, name) VALUES (1, 'legacy');
		INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (1, 'codex', '/legacy/scope', 1);
		CREATE TABLE unexpected(data TEXT);
	`)
	if invalid, err := library.Open(path); err == nil {
		_ = invalid.Close()
		t.Fatal("Open accepted an unexpected table")
	}
	// 修复输入并继续按旧格式记录分组依据;失败的迁移不能留下只迁了一半的新表。
	editLibraryFixture(t, path, `
		DROP TABLE unexpected;
		INSERT INTO activation_groups(skill_id, agent, scope_path, group_id) VALUES (1, 'codex', '/legacy/scope', 1);
	`)
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	want := []library.Activation{{SkillID: 1, Scope: library.Scope{Agent: library.AgentCodex, Path: "/legacy/scope"}, Individual: true, ViaGroups: []library.GroupID{1}}}
	got, err := lib.ListActivations(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("activations after retry = %#v, %v; want %#v", got, err, want)
	}
}
