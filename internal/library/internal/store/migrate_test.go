package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/store/migrations"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func baselineSchema(t *testing.T) string {
	t.Helper()
	if want := fmt.Sprintf("testdata/%05d_schema.sql", oldestVersion); baselineFile != want {
		t.Fatalf("baselineFile = %q; want %q to match oldestVersion", baselineFile, want)
	}
	return readTestdata(t, filepath.Base(baselineFile))
}

func testLayout(t *testing.T) layout {
	t.Helper()
	l, err := currentLayout()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func plannedLayout(t *testing.T, target string) layout {
	t.Helper()
	l := testLayout(t)
	plan, err := planMigration(t.Context(), baselineSchema(t), target, l)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SQL == "" {
		t.Fatal("planned migration is empty")
	}
	return layout{schema: target, steps: append(slices.Clip(l.steps), migrations.Step{Version: l.version() + 1, Name: "planned", SQL: plan.SQL})}
}

func writeOldestLibrary(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	statements := baselineSchema(t) + readTestdata(t, fmt.Sprintf("%05d_data.sql", oldestVersion)) +
		fmt.Sprintf(`PRAGMA application_id = %d; PRAGMA user_version = %d;`, applicationID, oldestVersion)
	if _, err := db.Exec(statements); err != nil {
		t.Fatal(err)
	}
}

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type tableDump map[string][]string

func dumpTables(t *testing.T, db *sql.DB) (tableDump, map[string][]string) {
	t.Helper()
	ctx := t.Context()
	objects, err := schemaObjects(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	dump := tableDump{}
	columns := map[string][]string{}
	for name, object := range objects {
		if object.kind != "table" {
			continue
		}
		list, err := tableColumns(ctx, db, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range list {
			columns[name] = append(columns[name], column.name)
		}
	}
	for table := range columns {
		dump[table] = selectColumns(t, db, table, columns[table])
	}
	return dump, columns
}

func selectColumns(t *testing.T, db *sql.DB, table string, columns []string) []string {
	t.Helper()
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = "quote(" + quoteIdentifier(column) + ")"
	}
	rows, err := db.QueryContext(t.Context(), `SELECT `+strings.Join(quoted, ` || '|' || `)+` FROM `+quoteIdentifier(table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(lines)
	return lines
}

func sequences(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT name, seq FROM sqlite_sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var name string
		var seq int
		if err := rows.Scan(&name, &seq); err != nil {
			t.Fatal(err)
		}
		got[name] = seq
	}
	return got
}

func requireDataKept(t *testing.T, before tableDump, beforeColumns map[string][]string, after *sql.DB) {
	t.Helper()
	_, afterColumns := dumpTables(t, after)
	for table, columns := range beforeColumns {
		if afterColumns[table] == nil {
			continue
		}
		var kept []string
		for _, column := range columns {
			if slices.Contains(afterColumns[table], column) {
				kept = append(kept, column)
			}
		}
		want := before[table]
		if len(kept) != len(columns) {
			want = selectColumnsFromDump(t, before, beforeColumns, table, kept)
		}
		if got := selectColumns(t, after, table, kept); !reflect.DeepEqual(got, want) {
			t.Errorf("table %s after upgrade = %q; want %q", table, got, want)
		}
	}
}

func selectColumnsFromDump(t *testing.T, dump tableDump, columns map[string][]string, table string, kept []string) []string {
	t.Helper()
	var lines []string
	for _, row := range dump[table] {
		values := strings.Split(row, "|")
		var keptValues []string
		for i, column := range columns[table] {
			if slices.Contains(kept, column) {
				keptValues = append(keptValues, values[i])
			}
		}
		lines = append(lines, strings.Join(keptValues, "|"))
	}
	slices.Sort(lines)
	return lines
}

func TestUpgradeFromOldestVersionReachesSchemaSQL(t *testing.T) {
	ctx := t.Context()
	l := testLayout(t)
	db, err := memoryDatabase(ctx, baselineSchema(t), oldestVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := upgrade(ctx, db, l.steps, nil); err != nil {
		t.Fatal(err)
	}
	expected, err := memoryDatabase(ctx, l.schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer expected.Close()
	got, err := schemaFingerprint(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	want, err := schemaFingerprint(ctx, expected)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("跑完 migrations/ 的结构与 schema.sql 不一致：改了 schema.sql 就在仓库根目录运行 go run ./internal/library/internal/store/migrationgen <这一步做了什么>,生成升级后一起提交")
	}
}

func TestUpgradeKeepsEveryTableOfTheOldestVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	writeOldestLibrary(t, path)
	db := openRaw(t, path)
	before, beforeColumns := dumpTables(t, db)
	for table, rows := range before {
		if len(rows) == 0 {
			t.Errorf("testdata/%05d_data.sql has no rows in %s; add some so upgrades are checked against real data", oldestVersion, table)
		}
	}
	if err := validateForeignKeys(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	beforeSequences := sequences(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := open(path, testLayout(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	requireDataKept(t, before, beforeColumns, s.db)
	if got := sequences(t, s.db); !reflect.DeepEqual(got, beforeSequences) {
		t.Fatalf("sqlite_sequence after upgrade = %v; want %v", got, beforeSequences)
	}
}

func pinnedSchema(t *testing.T) string {
	t.Helper()
	schema := testLayout(t).schema
	for _, edit := range []struct{ old, new string }{
		{"    note TEXT NOT NULL DEFAULT '',", "    note TEXT NOT NULL DEFAULT '',\n    pinned INTEGER NOT NULL DEFAULT 0 CHECK(pinned IN (0, 1)), -- 置顶"},
		{"ON history_skills(skill_id);", "ON history_skills(skill_id, name);"},
		{"    detect_path TEXT NOT NULL DEFAULT ''", "    detect_path TEXT NOT NULL DEFAULT '' -- 检测目录"},
	} {
		if !strings.Contains(schema, edit.old) {
			t.Fatalf("schema.sql no longer contains %q; update the test edit", edit.old)
		}
		schema = strings.Replace(schema, edit.old, edit.new, 1)
	}
	return schema + "\nCREATE TABLE IF NOT EXISTS pins (\n    id INTEGER PRIMARY KEY AUTOINCREMENT -- 置顶记录\n);\n"
}

func TestOpenBacksUpAndUpgradesAnOlderLibrary(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "library.db")
	writeOldestLibrary(t, path)
	db := openRaw(t, path)
	before, beforeColumns := dumpTables(t, db)
	beforeSequences := sequences(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	l := plannedLayout(t, pinnedSchema(t))
	s, err := open(path, l)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var version, foreignKeys int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != l.version() {
		t.Fatalf("user_version = %d, %v; want %d", version, err, l.version())
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys after upgrade = %d, %v; want 1", foreignKeys, err)
	}
	requireDataKept(t, before, beforeColumns, s.db)
	if got := sequences(t, s.db); got["skills"] != beforeSequences["skills"] {
		t.Fatalf("skills sequence after rebuild = %d; want %d", got["skills"], beforeSequences["skills"])
	}
	if got := selectColumns(t, s.db, "skills", []string{"pinned"}); !reflect.DeepEqual(got, []string{"0", "0"}) {
		t.Fatalf("new column pinned = %q; want the default for every skill", got)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO skills (source_id, name) VALUES (1, 'delta') RETURNING id`).Scan(&id); err != nil || id != int64(beforeSequences["skills"]+1) {
		t.Fatalf("new skill id = %d, %v; want %d (removed ids are not reused)", id, err, beforeSequences["skills"]+1)
	}

	backupPath := filepath.Join(dir, fmt.Sprintf("library-v%d.db", oldestVersion))
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 && os.PathSeparator == '/' {
		t.Fatalf("backup mode = %v; want 0600", mode)
	}
	backup := openRaw(t, backupPath)
	if err := backup.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != oldestVersion {
		t.Fatalf("backup user_version = %d, %v; want %d", version, err, oldestVersion)
	}
	if got, _ := dumpTables(t, backup); !reflect.DeepEqual(got, before) {
		t.Fatalf("backup data = %v; want the library as it was before the upgrade", got)
	}
	if _, err := os.Stat(backupPath + ".partial"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial backup left behind: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := open(path, l)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}

func TestFailedUpgradeLeavesTheLibraryAsItWas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   string
		layout func(t *testing.T) layout
	}{
		{name: "statement fails halfway", layout: func(t *testing.T) layout {
			return brokenLayout(t, "ALTER TABLE skills ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;\nINSERT INTO missing_table VALUES (1);")
		}},
		{name: "foreign keys broken", layout: func(t *testing.T) layout {
			return brokenLayout(t, "DELETE FROM sources WHERE id = 1;")
		}},
		{name: "old library does not match its schema", edit: "DROP INDEX skill_scopes_global_scope;", layout: func(t *testing.T) layout {
			return plannedLayout(t, pinnedSchema(t))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			writeOldestLibrary(t, path)
			if tc.edit != "" {
				db := openRaw(t, path)
				if _, err := db.Exec(tc.edit); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s, err := open(path, tc.layout(t))
			if err == nil {
				_ = s.Close()
				t.Fatal("open succeeded; want the upgrade to fail")
			}
			if !errors.Is(err, ErrUnacceptable) {
				t.Fatalf("open error = %v; want ErrUnacceptable", err)
			}
			if after, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("failed upgrade changed the library file: %v", err)
			}
			if tc.edit == "" {
				s, err = open(path, testLayout(t))
				if err != nil {
					t.Fatal(err)
				}
				_ = s.Close()
			}
		})
	}
}

func brokenLayout(t *testing.T, statements string) layout {
	t.Helper()
	l := testLayout(t)
	return layout{schema: l.schema, steps: append(slices.Clip(l.steps), migrations.Step{Version: l.version() + 1, Name: "broken", SQL: statements})}
}

func TestBackupIsNotOverwrittenOnceAnotherProcessUpgraded(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "library.db")
	writeOldestLibrary(t, path)
	l := plannedLayout(t, pinnedSchema(t))
	s, err := open(path, l)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backupPath := filepath.Join(dir, fmt.Sprintf("library-v%d.db", oldestVersion))
	before, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := backupBeforeUpgrade(ctx, s.db, path, oldestVersion); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(backupPath); err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("backup of version %d was overwritten by the upgraded library: %v", oldestVersion, err)
	}
	if partial, _ := filepath.Glob(filepath.Join(dir, "*.partial")); len(partial) > 0 {
		t.Fatalf("partial backups left behind: %v", partial)
	}
}

func TestOpenThroughFileURIBacksUpTheDatabaseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library.db")
	writeOldestLibrary(t, path)
	s, err := open("file:"+filepath.ToSlash(path), plannedLayout(t, pinnedSchema(t)))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("library-v%d.db", oldestVersion))); err != nil {
		t.Fatalf("no backup next to the database opened through a file: URI: %v", err)
	}
}

func TestDatabaseFile(t *testing.T) {
	for _, tc := range []struct {
		path, want string
		ok         bool
	}{
		{"/data/library.db", "/data/library.db", true},
		{":memory:", "", false},
		{"file::memory:?cache=shared", "", false},
		{"file:/data/library.db?mode=memory", "", false},
		{"file:/data/library.db?_txlock=immediate", "/data/library.db", true},
		{"file:///data/library.db", "/data/library.db", true},
		{"file:library.db", "library.db", true},
	} {
		got, ok := databaseFile(tc.path)
		if want := filepath.FromSlash(tc.want); got != want || ok != tc.ok {
			t.Errorf("databaseFile(%q) = %q, %v; want %q, %v", tc.path, got, ok, want, tc.ok)
		}
	}
}

func TestUpgradeSkipsStepsAnotherProcessAlreadyRan(t *testing.T) {
	ctx := t.Context()
	l := plannedLayout(t, pinnedSchema(t))
	db, err := memoryDatabase(ctx, baselineSchema(t), oldestVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for range 2 {
		if err := upgrade(ctx, db, l.steps, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenImageUpgradesBackupFromOlderVersion(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	writeOldestLibrary(t, path)
	old, err := open(path, testLayout(t))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	data, _, err := old.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	l := plannedLayout(t, pinnedSchema(t))
	image, err := openImage(ctx, data, l)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	var version int
	if err := image.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != l.version() {
		t.Fatalf("pulled user_version = %d, %v; want %d", version, err, l.version())
	}
	summary, err := image.Summary(ctx)
	if err != nil || summary.Skills != 2 || summary.Groups != 1 {
		t.Fatalf("pulled summary = %+v, %v; want 2 skills and 1 group", summary, err)
	}
}

func TestPlanMigrationRefusesWhatItCannotFill(t *testing.T) {
	schema := strings.Replace(testLayout(t).schema, "    note TEXT NOT NULL DEFAULT '',", "    note TEXT NOT NULL DEFAULT '',\n    owner TEXT NOT NULL,", 1)
	_, err := planMigration(t.Context(), baselineSchema(t), schema, testLayout(t))
	if err == nil || !strings.Contains(err.Error(), "skills.owner") {
		t.Fatalf("plan error = %v; want it to name skills.owner", err)
	}
	schema = strings.Replace(testLayout(t).schema, "    group_id INTEGER NOT NULL REFERENCES groups(id),", "    group_id INTEGER NOT NULL REFERENCES groups(id),\n    tenant TEXT NOT NULL,", 1)
	schema = strings.Replace(schema, "PRIMARY KEY(skill_id, group_id)", "PRIMARY KEY(skill_id, group_id, tenant)", 1)
	_, err = planMigration(t.Context(), baselineSchema(t), schema, testLayout(t))
	if err == nil || !strings.Contains(err.Error(), "skill_groups.tenant") {
		t.Fatalf("plan error = %v; want it to name the new primary key column skill_groups.tenant", err)
	}
}

func TestPlanMigrationWarnsAboutDroppedData(t *testing.T) {
	schema := testLayout(t).schema
	schema = strings.Replace(schema, "    note TEXT NOT NULL DEFAULT '',                     -- 用户写的备注,只供展示,不进技能文件;空 = 没有备注\n", "", 1)
	start := strings.Index(schema, "CREATE TABLE IF NOT EXISTS rejected_renames")
	end := start + strings.Index(schema[start:], ");") + 2
	schema = schema[:start] + schema[end:]
	plan, err := planMigration(t.Context(), baselineSchema(t), schema, testLayout(t))
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(plan.Warnings, "\n")
	for _, want := range []string{"skills.note", "rejected_renames"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings = %q; want one about %s", warnings, want)
		}
	}
}

func TestPlanMigrationIsEmptyWhenSchemaIsUnchanged(t *testing.T) {
	l := testLayout(t)
	plan, err := planMigration(t.Context(), baselineSchema(t), l.schema, l)
	if err != nil || plan.SQL != "" {
		t.Fatalf("plan = %+v, %v; want an empty plan", plan, err)
	}
}

func TestPlanMigrationNamesTheNextFile(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{baselineFile: baselineSchema(t), "schema.sql": pinnedSchema(t)} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := PlanMigration(t.Context(), dir, "add_pins")
	if want := filepath.Join("migrations", fmt.Sprintf("%05d_add_pins.sql", testLayout(t).version()+1)); err != nil || plan.File != want {
		t.Fatalf("PlanMigration file = %q, %v; want %q", plan.File, err, want)
	}
	if _, err := PlanMigration(t.Context(), dir, "AddPins"); err == nil {
		t.Fatal("PlanMigration accepted a name that Load would reject")
	}
}
