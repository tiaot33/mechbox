package library_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 打开数据库时只插入应用收录的、表里还没有的 Agent,放进候选池;表里已有的行(包括用户改过名称和目录的)从不改写,
// 装在那里的技能照旧按改过的目录算。
func TestOpenInsertsMissingAgentsButNeverRewritesExistingOnes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	setTestHome(t, home)
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openTestLibraryAt(t, path)
	ctx := t.Context()
	skill := fetchNamedSkill(t, lib, "example")
	if err := lib.Install(ctx, skill.ID, library.GlobalScope(library.AgentClaudeCode), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	pool, err := lib.ListAgentPool(ctx)
	if err != nil || len(pool) != 73 {
		t.Fatalf("ListAgentPool() = %d agents, %v; want the 73 collected agents not added yet", len(pool), err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	// 用 SQL 改出用户改过的 Claude Code、删掉候选池里的 Cursor,相当于旧版应用写下的库;结果经技能库公开方法观察。
	editLibraryFixture(t, path, `
		DELETE FROM agents WHERE code = 'cursor';
		UPDATE agents SET name = 'My Claude', global_paths = '[".claude/skills", ".mine/skills"]', detect_paths = '[]' WHERE code = 'claude-code';
	`)
	claude := seededAgents(home)[0]
	claude.Name, claude.DetectDirectories = "My Claude", []string{}
	claude.GlobalDirectories = append(claude.GlobalDirectories, filepath.Join(home, ".mine", "skills"))
	for range 2 {
		reopened := openTestLibraryAt(t, path)
		agents := listAgents(t, reopened)
		if len(agents) != 2 || !reflect.DeepEqual(agents[0], claude) {
			t.Fatalf("ListAgents after Open = %#v; want %#v kept as edited", agents, claude)
		}
		pool, err := reopened.ListAgentPool(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cursor := slices.IndexFunc(pool, func(agent library.Agent) bool { return agent.Code == "cursor" })
		if cursor < 0 || pool[cursor].Name != "Cursor" || !reflect.DeepEqual(pool[cursor].GlobalDirectories, []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".cursor", "skills")}) {
			t.Fatalf("ListAgentPool after Open = %#v; want Cursor back in the pool", pool)
		}
		requireSkillScopes(t, reopened, []library.SkillScope{
			{SkillID: skill.ID, Scope: library.GlobalScope(library.AgentClaudeCode), Folder: filepath.Join(home, ".claude", "skills", "example"), Direct: true},
		})
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// 库里有、本版应用不认识的 Agent(例如从装了更新版本应用的机器同步来)保持原样:照样列出,目录按库里的规则算。
func TestOpenKeepsAgentsUnknownToThisVersion(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	setTestHome(t, home)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path, testOptions(t)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, `INSERT INTO agents(code, name, global_paths, project_paths, enabled) VALUES ('future-agent', 'Future Agent', '[".future/skills"]', '[".future/project-skills"]', 1)`)
	reopened := openTestLibraryAt(t, path)
	ctx := t.Context()
	agents, err := reopened.ListAgents(ctx)
	want := append(seededAgents(home), library.Agent{
		Code: "future-agent", Name: "Future Agent", DetectDirectories: []string{},
		GlobalDirectories: []string{filepath.Join(home, ".future", "skills")}, ProjectDirectories: []string{".future/project-skills"},
	})
	if err != nil || !reflect.DeepEqual(agents, want) {
		t.Fatalf("ListAgents after Open = %#v, %v; want %#v", agents, err, want)
	}
	directory, err := reopened.ScopeDirectory(ctx, library.GlobalScope("future-agent"))
	if wantDirectory := filepath.Join(home, ".future", "skills"); err != nil || directory != wantDirectory {
		t.Fatalf("ScopeDirectory(future-agent) = %q, %v; want %q", directory, err, wantDirectory)
	}
}

func TestOpenRejectsMissingScopeIdentityConstraint(t *testing.T) {
	t.Parallel()
	for _, index := range []string{"skill_scopes_global_scope", "skill_scopes_project_scope", "group_installs_global_scope", "group_installs_project_scope"} {
		t.Run(index, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			lib, err := library.Open(path, testOptions(t)...)
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
			invalid, err := library.Open(path, testOptions(t)...)
			if err == nil {
				_ = invalid.Close()
			}
			requireLibraryError(t, err, library.FailedPrecondition)
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected Open changed the library file: %v", err)
			}
		})
	}
}

// 同一 schema 版本下少了表的库同样按表结构不符拒绝,文件保持不变。
func TestOpenRejectsLibraryMissingATable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path, testOptions(t)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, `DROP TABLE replica`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := library.Open(path, testOptions(t)...)
	if err == nil {
		_ = invalid.Close()
	}
	requireLibraryError(t, err, library.FailedPrecondition)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected Open changed the library file: %v", err)
	}
}

// v0.0.6(schema 版本 27)之前的库不升级,比本版应用新的库不认:两种都被拒绝,文件保持不变。
func TestOpenRejectsUnsupportedSchemaVersionsWithoutChangingTheFile(t *testing.T) {
	t.Parallel()
	for _, version := range []int{24, 9999} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			lib, err := library.Open(path, testOptions(t)...)
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.Close(); err != nil {
				t.Fatal(err)
			}
			editLibraryFixture(t, path, fmt.Sprintf(`PRAGMA user_version = %d`, version))
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			unsupported, err := library.Open(path, testOptions(t)...)
			if err == nil {
				_ = unsupported.Close()
			}
			requireLibraryError(t, err, library.FailedPrecondition)
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected Open changed the library file: %v", err)
			}
			if backups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "library-v*.db")); len(backups) > 0 {
				t.Fatalf("rejected Open left backups %v", backups)
			}
		})
	}
}

// 用户选错了文件(不是 SQLite 数据库)时,Open 返回 FailedPrecondition,文件保持不变。
func TestOpenRejectsNonDatabaseFileWithoutChangingIt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "notes.txt")
	before := bytes.Repeat([]byte("not a sqlite database\n"), 256)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(path, testOptions(t)...)
	if err == nil {
		_ = lib.Close()
	}
	requireLibraryError(t, err, library.FailedPrecondition)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected Open changed the file: %v", err)
	}
}

func TestOpenRejectsForeignDatabaseWithoutMutatingIt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "foreign.db")
	foreign, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Exec(`CREATE TABLE notes (body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(path, testOptions(t)...)
	if err == nil {
		_ = lib.Close()
	}
	requireLibraryError(t, err, library.FailedPrecondition)
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("foreign database changed: %v", err)
	}
}
