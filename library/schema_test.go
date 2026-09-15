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

// 旧库里以绝对路径记录的 Activation 在 Open 时映射成 Scope 身份:路径等于某 Agent 现行布局
// (或本产品 #44 之前写过的旧目录 .codex/skills)在当前 $HOME / 某已登记项目下算出的目录即可识别;
// 映射到同一身份的多行合并保留全部依据;无法识别的行被丢弃,磁盘上任何文件夹都不动。
func TestOpenMigratesLegacyLibraryAndPreservesActivationOrigins(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			t.Setenv("HOME", home)
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
			// v3 库里 claude-code 带自定义布局且缺 codex 行:迁移按表里的规则识别路径,缺行补种子。
			claudeGlobal, claudeProject := filepath.Join(".claude", "skills"), filepath.Join(".claude", "skills")
			if version == 3 {
				claudeGlobal, claudeProject = filepath.Join(".custom", "global"), filepath.Join(".custom", "project")
				editLibraryFixture(t, path, `INSERT INTO agents(code, global_path, project_path) VALUES ('claude-code', '.custom/global', '.custom/project')`)
			}
			projectPath := t.TempDir()
			editLibraryFixture(t, path, `INSERT INTO projects(id, path) VALUES (1, ?)`, projectPath)
			legacyRows := []struct {
				agent      string
				path       string
				individual bool
				groups     []library.GroupID
			}{
				{"codex", filepath.Join(projectPath, ".codex", "skills"), true, []library.GroupID{1, 2}}, // 旧目录名 → 项目 Scope
				{"codex", filepath.Join(projectPath, "custom"), false, []library.GroupID{2}},             // 无法识别 → 丢弃
				{"codex", filepath.Join(home, ".codex", "skills"), true, nil},                            // 旧目录名 → 全局 Scope
				{"codex", filepath.Join(home, ".agents", "skills"), false, []library.GroupID{1}},         // 现行目录 → 同一全局 Scope,合并
				{"claude-code", filepath.Join(home, claudeGlobal), true, nil},                            // 按表里的规则识别
				{"cursor", filepath.Join(home, ".cursor", "skills"), true, nil},                          // 未知 Agent → 丢弃
			}
			for _, row := range legacyRows {
				editLibraryFixture(t, path, `INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (1, ?, ?, ?)`, row.agent, row.path, row.individual)
				for _, groupID := range row.groups {
					if version >= 3 {
						editLibraryFixture(t, path, `
							INSERT INTO activation_groups(activation_id, skill_id, agent, scope_path, group_id)
							SELECT id, skill_id, agent, scope_path, ? FROM activations WHERE scope_path = ?
						`, groupID, row.path)
					} else {
						editLibraryFixture(t, path, `INSERT INTO activation_groups(skill_id, agent, scope_path, group_id) VALUES (1, ?, ?, ?)`, row.agent, row.path, groupID)
					}
				}
				writeFile(t, filepath.Join(row.path, "example", "SKILL.md"), []byte("example\n"), 0o644)
			}
			if version >= 3 {
				// 已按身份记录的行原样保留(路径快照与之无关)。
				editLibraryFixture(t, path, `INSERT INTO activations(skill_id, agent, project_id, scope_kind, scope_path, individual) VALUES (1, 'claude-code', 1, 'project', '/stale/snapshot', 0)`)
				editLibraryFixture(t, path, `INSERT INTO activation_groups(activation_id, skill_id, agent, scope_path, group_id) SELECT id, skill_id, agent, scope_path, 2 FROM activations WHERE scope_path = '/stale/snapshot'`)
			}

			lib, err := library.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lib.Close() })
			codexGlobal := library.GlobalScope{Agent: library.AgentCodex}
			codexProject := library.ProjectScope{Agent: library.AgentCodex, ProjectID: 1}
			want := []library.Activation{
				{SkillID: 1, Scope: library.GlobalScope{Agent: library.AgentClaudeCode}, Directory: filepath.Join(home, claudeGlobal), Individual: true},
			}
			if version >= 3 {
				want = append(want, library.Activation{SkillID: 1, Scope: library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: 1}, Directory: filepath.Join(projectPath, claudeProject), ViaGroups: []library.GroupID{2}})
			}
			want = append(want,
				library.Activation{SkillID: 1, Scope: codexGlobal, Directory: filepath.Join(home, ".agents", "skills"), Individual: true, ViaGroups: []library.GroupID{1}},
				library.Activation{SkillID: 1, Scope: codexProject, Directory: filepath.Join(projectPath, ".agents", "skills"), Individual: true, ViaGroups: []library.GroupID{1, 2}},
			)
			got, err := lib.ListActivations(ctx)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("migrated activations = %#v, %v; want %#v", got, err, want)
			}
			// 迁移不搬迁、不删除任何旧目录里的文件夹,包括被丢弃行对应的那些。
			for _, row := range legacyRows {
				if _, err := os.Stat(filepath.Join(row.path, "example", "SKILL.md")); err != nil {
					t.Fatalf("migration touched projected copy at %s: %v", row.path, err)
				}
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

			// 迁移后 Agent 表完整,身份记录可直接再次激活(幂等)并继续参与操作:
			// 撤组依据、撤独立依据、删组转独立保留。
			agents, err := lib.ListAgents(ctx)
			if err != nil || !reflect.DeepEqual(agents, seededAgents()) {
				t.Fatalf("ListAgents() after migration = %#v, %v", agents, err)
			}
			if err := lib.Activate(ctx, 1, codexGlobal, library.ActivateOptions{}); err != nil {
				t.Fatalf("re-Activate migrated global scope = %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "example", "SKILL.md")); err != nil {
				t.Fatalf("re-Activate did not project into the current directory: %v", err)
			}
			if err := lib.DeactivateGroup(ctx, 2, codexProject); err != nil {
				t.Fatal(err)
			}
			if err := lib.Deactivate(ctx, 1, codexGlobal); err != nil {
				t.Fatal(err)
			}
			if err := lib.DeleteGroup(ctx, 1); err != nil {
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
			for i := range want {
				want[i].ViaGroups = nil
				if want[i].Scope == codexGlobal || want[i].Scope == codexProject {
					want[i].Individual = true
				}
			}
			if version >= 3 {
				want[1].ViaGroups = []library.GroupID{2}
			}
			got, err = reopened.ListActivations(ctx)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("reopened activations = %#v, %v; want %#v", got, err, want)
			}
		})
	}
}

// v5 库 Open 时只给 skill_versions 加确认 commit 列(空 = 未知,走完整比较),
// 已有 Skill、版本、tree SHA 与身份型 Activation 全部原样保留;迁移后的库与新建库指纹一致,可反复 Open。
func TestOpenMigratesV5LibraryAndPreservesSkillsVersionsAndActivations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	schema, err := os.ReadFile("testdata/v5.sql")
	if err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, string(schema))
	editLibraryFixture(t, path, `
		PRAGMA application_id = 1397443650;
		PRAGMA user_version = 5;
		INSERT INTO agents(code, name, global_path, project_path) VALUES
			('claude-code', 'Claude Code', '.claude/skills', '.claude/skills'),
			('codex', 'Codex', '.agents/skills', '.agents/skills');
		INSERT INTO sources(id, type, locator, last_fetched, last_seen_ref) VALUES
			(1, 'github', '{"repository":"https://github.com/owner/repo.git","branch":"main"}', '2026-01-01T00:00:00Z', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb');
		INSERT INTO skills(id, source_id, name, current_version_id) VALUES (1, 1, 'example', 2);
		INSERT INTO skill_versions(id, skill_id, git_tree_sha) VALUES
			(1, 1, ''),
			(2, 1, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa');
		INSERT INTO blobs(fingerprint, content) VALUES ('old', X'6F6C640A'), ('new', X'6E65770A');
		INSERT INTO version_files(version_id, path, fingerprint, mode) VALUES
			(1, 'SKILL.md', 'old', 'regular'),
			(2, 'SKILL.md', 'new', 'regular'),
			(2, 'run', 'new', 'executable');
		INSERT INTO groups(id, name) VALUES (1, 'legacy');
		INSERT INTO skill_groups(skill_id, group_id) VALUES (1, 1);
		INSERT INTO activations(id, skill_id, agent, project_id, individual) VALUES (1, 1, 'codex', NULL, 1);
		INSERT INTO activation_groups(activation_id, group_id) VALUES (1, 1);
	`)
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	skill, err := lib.GetSkill(ctx, 1)
	if err != nil || skill.Name != "example" || skill.GitTreeSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || skill.Source.LastSeenRef != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("migrated skill = %#v, %v", skill, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte("new\n"), Mode: library.ModeRegular},
		{Path: "run", Content: []byte("new\n"), Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(skill.Files, wantFiles) {
		t.Fatalf("migrated files = %#v, want %#v", skill.Files, wantFiles)
	}
	versions, err := lib.ListVersions(ctx, 1)
	if err != nil || len(versions) != 2 || versions[0].GitTreeSHA != "" || versions[1].GitTreeSHA != skill.GitTreeSHA {
		t.Fatalf("migrated versions = %#v, %v", versions, err)
	}
	first, err := lib.GetVersion(ctx, versions[0].ID)
	if err != nil || len(first.Files) != 1 || string(first.Files[0].Content) != "old\n" {
		t.Fatalf("migrated first version = %#v, %v", first, err)
	}
	wantActivations := []library.Activation{{
		SkillID:    1,
		Scope:      library.GlobalScope{Agent: library.AgentCodex},
		Directory:  filepath.Join(home, ".agents", "skills"),
		Individual: true,
		ViaGroups:  []library.GroupID{1},
	}}
	if got, err := lib.ListActivations(ctx); err != nil || !reflect.DeepEqual(got, wantActivations) {
		t.Fatalf("migrated activations = %#v, %v; want %#v", got, err, wantActivations)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := library.Open(path)
	if err != nil {
		t.Fatalf("reopen migrated library: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got, err := reopened.ListActivations(ctx); err != nil || !reflect.DeepEqual(got, wantActivations) {
		t.Fatalf("reopened activations = %#v, %v", got, err)
	}
}

// Open 只补缺失的 Agent 行、不覆盖已有布局;已按身份记录的 Activation 在重开后按现行规则派生目录,
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
		library.GlobalScope{Agent: library.AgentClaudeCode},
		library.GlobalScope{Agent: library.AgentCodex},
		library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID},
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
	want := []library.Activation{
		{SkillID: skill.ID, Scope: scopes[0], Directory: filepath.Join(home, ".custom", "global"), Individual: true},
		{SkillID: skill.ID, Scope: scopes[1], Directory: filepath.Join(home, ".agents", "skills"), Individual: true},
		{SkillID: skill.ID, Scope: scopes[2], Directory: filepath.Join(project.Path, ".agents", "skills"), Individual: true},
	}
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
	home := t.TempDir()
	t.Setenv("HOME", home)
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
		CREATE TABLE unexpected(data TEXT);
	`)
	directory := filepath.Join(home, ".agents", "skills")
	editLibraryFixture(t, path, `INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (1, 'codex', ?, 1)`, directory)
	if invalid, err := library.Open(path); err == nil {
		_ = invalid.Close()
		t.Fatal("Open accepted an unexpected table")
	}
	// 修复输入并继续按旧格式记录分组依据;失败的迁移不能留下只迁了一半的新表。
	editLibraryFixture(t, path, `DROP TABLE unexpected`)
	editLibraryFixture(t, path, `INSERT INTO activation_groups(skill_id, agent, scope_path, group_id) VALUES (1, 'codex', ?, 1)`, directory)
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	want := []library.Activation{{SkillID: 1, Scope: library.GlobalScope{Agent: library.AgentCodex}, Directory: directory, Individual: true, ViaGroups: []library.GroupID{1}}}
	got, err := lib.ListActivations(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("activations after retry = %#v, %v; want %#v", got, err, want)
	}
}

// 旧库路径带末尾 / 或 /. 时仍应识别为对应身份,不能丢掉独立与分组依据。
func TestOpenMigratesLexicallyEquivalentLegacyPaths(t *testing.T) {
	for _, suffix := range []string{"/", "/."} {
		t.Run(suffix, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "library.db")
			schema, err := os.ReadFile("testdata/v2.sql")
			if err != nil {
				t.Fatal(err)
			}
			editLibraryFixture(t, path, string(schema))
			editLibraryFixture(t, path, `
				PRAGMA application_id = 1397443650;
				PRAGMA user_version = 2;
				INSERT INTO sources(id, type, locator) VALUES (1, 'local', '{"path":"/source"}');
				INSERT INTO skills(id, source_id, name) VALUES (1, 1, 'example');
				INSERT INTO groups(id, name) VALUES (1, 'legacy');
			`)
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(home, ".claude", "skills") + suffix
			editLibraryFixture(t, path, `INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (1, 'claude-code', ?, 1)`, directory)
			editLibraryFixture(t, path, `INSERT INTO activation_groups(skill_id, agent, scope_path, group_id) VALUES (1, 'claude-code', ?, 1)`, directory)
			lib, err := library.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lib.Close() })
			want := []library.Activation{{
				SkillID:    1,
				Scope:      library.GlobalScope{Agent: library.AgentClaudeCode},
				Directory:  filepath.Join(home, ".claude", "skills"),
				Individual: true,
				ViaGroups:  []library.GroupID{1},
			}}
			got, err := lib.ListActivations(ctx)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("recognizable legacy path %q = %#v, %v; want %#v", directory, got, err, want)
			}
		})
	}
}
