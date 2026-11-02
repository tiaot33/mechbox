package store

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/store/migrations"
)

type MigrationPlan struct {
	Version  int
	SQL      string
	Warnings []string
}

func PlanMigration(ctx context.Context, baseline, target string) (MigrationPlan, error) {
	l, err := currentLayout()
	if err != nil {
		return MigrationPlan{}, err
	}
	return planMigration(ctx, baseline, target, l.steps)
}

type schemaObject struct {
	kind, name, table, sql string
}

type tableColumn struct {
	name     string
	required bool
}

func planMigration(ctx context.Context, baseline, target string, steps []migrations.Step) (MigrationPlan, error) {
	from, err := memoryDatabase(ctx, baseline, oldestVersion)
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("load baseline schema: %w", err)
	}
	defer from.Close()
	if err := upgrade(ctx, from, steps); err != nil {
		return MigrationPlan{}, err
	}
	to, err := memoryDatabase(ctx, target, 0)
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("load schema.sql: %w", err)
	}
	defer to.Close()
	plan := MigrationPlan{Version: oldestVersion + len(steps) + 1}
	statements, warnings, err := diffSchemas(ctx, from, to)
	if err != nil || len(statements) == 0 {
		return plan, err
	}
	plan.SQL = strings.Join(statements, "\n\n") + "\n"
	plan.Warnings = warnings
	if err := upgrade(ctx, from, []migrations.Step{{Version: plan.Version, Name: "planned", SQL: plan.SQL}}); err != nil {
		return MigrationPlan{}, fmt.Errorf("apply planned migration: %w", err)
	}
	got, err := schemaFingerprint(ctx, from)
	if err != nil {
		return MigrationPlan{}, err
	}
	want, err := schemaFingerprint(ctx, to)
	if err != nil {
		return MigrationPlan{}, err
	}
	if got != want {
		return MigrationPlan{}, fmt.Errorf("planned migration does not reproduce schema.sql")
	}
	return plan, nil
}

func memoryDatabase(ctx context.Context, schema string, version int) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func diffSchemas(ctx context.Context, from, to *sql.DB) ([]string, []string, error) {
	fromObjects, err := schemaObjects(ctx, from)
	if err != nil {
		return nil, nil, err
	}
	toObjects, err := schemaObjects(ctx, to)
	if err != nil {
		return nil, nil, err
	}
	var statements, warnings []string
	tables := map[string]bool{}
	for name, object := range fromObjects {
		if object.kind == "table" {
			tables[name] = true
		}
	}
	for name, object := range toObjects {
		if object.kind == "table" {
			tables[name] = true
		}
	}
	for _, table := range slices.Sorted(maps.Keys(tables)) {
		before, inFrom := fromObjects[table]
		after, inTo := toObjects[table]
		switch {
		case !inFrom:
			statements = append(statements, after.sql+";")
			statements = append(statements, indexStatements(toObjects, table)...)
		case !inTo:
			statements = append(statements, "DROP TABLE "+table+";")
			warnings = append(warnings, fmt.Sprintf("删除表 %s 和其中的数据;如果是改名，请手改生成的文件", table))
		case normalizeStatement(table, before.sql) != normalizeStatement(table, after.sql):
			rebuild, rebuildWarnings, err := rebuildTable(ctx, from, to, table, before, after)
			if err != nil {
				return nil, nil, err
			}
			statements = append(statements, rebuild...)
			statements = append(statements, indexStatements(toObjects, table)...)
			warnings = append(warnings, rebuildWarnings...)
		default:
			statements = append(statements, indexChanges(fromObjects, toObjects, table)...)
		}
	}
	return statements, warnings, nil
}

func schemaObjects(ctx context.Context, db *sql.DB) (map[string]schemaObject, error) {
	rows, err := db.QueryContext(ctx, `SELECT type, name, tbl_name, sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := map[string]schemaObject{}
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.kind, &object.name, &object.table, &object.sql); err != nil {
			return nil, err
		}
		if object.kind != "table" && object.kind != "index" {
			return nil, fmt.Errorf("schema has a %s (%s); only tables and indexes are supported", object.kind, object.name)
		}
		objects[object.name] = object
	}
	return objects, rows.Err()
}

func indexStatements(objects map[string]schemaObject, table string) []string {
	var statements []string
	for _, name := range slices.Sorted(maps.Keys(objects)) {
		if object := objects[name]; object.kind == "index" && object.table == table {
			statements = append(statements, object.sql+";")
		}
	}
	return statements
}

func indexChanges(fromObjects, toObjects map[string]schemaObject, table string) []string {
	names := map[string]bool{}
	for _, objects := range []map[string]schemaObject{fromObjects, toObjects} {
		for name, object := range objects {
			if object.kind == "index" && object.table == table {
				names[name] = true
			}
		}
	}
	var statements []string
	for _, name := range slices.Sorted(maps.Keys(names)) {
		before, inFrom := fromObjects[name]
		after, inTo := toObjects[name]
		if inFrom && inTo && normalizeStatement(name, before.sql) == normalizeStatement(name, after.sql) {
			continue
		}
		if inFrom {
			statements = append(statements, "DROP INDEX "+name+";")
		}
		if inTo {
			statements = append(statements, after.sql+";")
		}
	}
	return statements
}

func rebuildTable(ctx context.Context, from, to *sql.DB, table string, before, after schemaObject) ([]string, []string, error) {
	created, err := renameCreateTable(after.sql, table, "new_"+table)
	if err != nil {
		return nil, nil, err
	}
	oldColumns, err := tableColumns(ctx, from, table)
	if err != nil {
		return nil, nil, err
	}
	newColumns, err := tableColumns(ctx, to, table)
	if err != nil {
		return nil, nil, err
	}
	var kept []string
	var warnings []string
	for _, column := range newColumns {
		if slices.ContainsFunc(oldColumns, func(old tableColumn) bool { return old.name == column.name }) {
			kept = append(kept, column.name)
		} else if column.required {
			return nil, nil, fmt.Errorf("新列 %s.%s 是 NOT NULL 又没有 DEFAULT,旧数据没法填，这一步要手写", table, column.name)
		}
	}
	for _, column := range oldColumns {
		if !slices.Contains(kept, column.name) {
			warnings = append(warnings, fmt.Sprintf("删除列 %s.%s 和其中的数据;如果是改名，请手改生成的文件里复制数据的那句", table, column.name))
		}
	}
	quoted := make([]string, len(kept))
	for i, name := range kept {
		quoted[i] = quoteIdentifier(name)
	}
	columns := strings.Join(quoted, ", ")
	statements := []string{
		created + ";",
		fmt.Sprintf("INSERT INTO new_%s (%s)\nSELECT %s FROM %s;", table, columns, columns, table),
	}
	if autoincrement(before.sql) && autoincrement(after.sql) {
		statements = append(statements, fmt.Sprintf("DELETE FROM sqlite_sequence WHERE name = 'new_%s';\nINSERT INTO sqlite_sequence (name, seq) SELECT 'new_%s', seq FROM sqlite_sequence WHERE name = '%s';", table, table, table))
	}
	statements = append(statements, fmt.Sprintf("DROP TABLE %s;\nALTER TABLE new_%s RENAME TO %s;", table, table, table))
	return statements, warnings, nil
}

func renameCreateTable(statement, table, name string) (string, error) {
	for _, prefix := range []string{"CREATE TABLE " + table, `CREATE TABLE "` + table + `"`} {
		if rest, ok := strings.CutPrefix(statement, prefix); ok && (rest == "" || !isIdentifierByte(rest[0])) {
			return "CREATE TABLE " + name + rest, nil
		}
	}
	return "", fmt.Errorf("cannot rename CREATE TABLE statement of %s", table)
}

func isIdentifierByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func autoincrement(statement string) bool {
	return strings.Contains(strings.ToUpper(statement), "AUTOINCREMENT")
}

func tableColumns(ctx context.Context, db *sql.DB, table string) ([]tableColumn, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, "notnull", dflt_value IS NULL, pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []tableColumn
	for rows.Next() {
		var column tableColumn
		var notNull, noDefault bool
		var pk int
		if err := rows.Scan(&column.name, &notNull, &noDefault, &pk); err != nil {
			return nil, err
		}
		column.required = notNull && noDefault && pk == 0
		columns = append(columns, column)
	}
	return columns, rows.Err()
}
