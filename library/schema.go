package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	libraryApplicationID = 1397443650 // "SKLB"
	librarySchemaVersion = 4
	agentSeedSQL         = `
		INSERT INTO agents(code, name, global_path, project_path) VALUES
			('claude-code', 'Claude Code', '.claude/skills', '.claude/skills'),
			('codex', 'Codex', '.agents/skills', '.agents/skills')
		ON CONFLICT(code) DO NOTHING
	`
)

//go:embed schema.sql
var schemaSQL string

// seedAgents 只补缺失代码,已有名称与布局即使不同也不覆盖;库校验失败则撤销补种子。
func seedAgents(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, agentSeedSQL); err != nil {
		return err
	}
	if err := validateLibrarySchema(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// schemaReader 让 Open/Sync 的连接校验与迁移提交前的事务校验共用同一套规则。
type schemaReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// errUnacceptableSchema 只标记已确认的库校验失败,不包含本地 I/O 或取消。
var errUnacceptableSchema = errors.New("unacceptable library schema")

func validateLibrarySchema(ctx context.Context, db schemaReader) error {
	var applicationID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	if applicationID != libraryApplicationID || userVersion != librarySchemaVersion {
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
	actual, err := schemaFingerprint(ctx, db)
	if err != nil {
		return err
	}
	expectedDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return err
	}
	defer expectedDB.Close()
	if _, err := expectedDB.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	expected, err := schemaFingerprint(ctx, expectedDB)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: schema fingerprint mismatch", errUnacceptableSchema)
	}
	return validateForeignKeys(ctx, db)
}

func validateForeignKeys(ctx context.Context, db schemaReader) error {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("%w: foreign key check failed", errUnacceptableSchema)
	}
	return rows.Err()
}

func schemaFingerprint(ctx context.Context, db schemaReader) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\n%s\n", name, strings.Join(strings.Fields(statement), " "))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// migrateLibrary 把已有库升到当前 schema;身份不符则不改文件。
func migrateLibrary(ctx context.Context, db *sql.DB) error {
	var applicationID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	if applicationID != libraryApplicationID {
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
	switch userVersion {
	case librarySchemaVersion:
		return nil
	case 1, 2:
		return migrateLegacyLibrary(ctx, db, userVersion)
	case 3:
		return migrateLibraryV3ToV4(ctx, db)
	default:
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
}

// migrateLegacyLibrary 在一个事务内扩展 v1/v2 库;旧路径暂不推断为全局或项目身份。
func migrateLegacyLibrary(ctx context.Context, db *sql.DB, version int) error {
	if err := validateForeignKeys(ctx, db); err != nil {
		return err
	}
	var statements []string
	if version == 1 {
		createSQL, err := canonicalTableSQL(ctx, "skill_versions")
		if err != nil {
			return fmt.Errorf("migrate library schema: %w", err)
		}
		statements = append(statements,
			`CREATE TABLE skill_versions_migrate AS SELECT id, skill_id FROM skill_versions`,
			`DROP TABLE skill_versions`, createSQL,
			`INSERT INTO skill_versions(id, skill_id) SELECT id, skill_id FROM skill_versions_migrate`,
			`DROP TABLE skill_versions_migrate`,
		)
	}
	statements = append(statements,
		`CREATE TABLE activations_migrate AS SELECT skill_id, agent, scope_path, individual FROM activations`,
		`CREATE TABLE activation_groups_migrate AS SELECT skill_id, agent, scope_path, group_id FROM activation_groups`,
		`DROP TABLE activation_groups`,
		`DROP TABLE activations`,
		schemaSQL,
		agentSeedSQL,
		`INSERT INTO activations(skill_id, agent, scope_path, individual)
		 SELECT skill_id, agent, scope_path, individual FROM activations_migrate`,
		`INSERT INTO activation_groups(activation_id, skill_id, agent, scope_path, group_id)
		 SELECT a.id, g.skill_id, g.agent, g.scope_path, g.group_id
		 FROM activation_groups_migrate g JOIN activations a
		 ON a.skill_id = g.skill_id AND a.agent = g.agent AND a.scope_path = g.scope_path`,
		`DROP TABLE activation_groups_migrate`,
		`DROP TABLE activations_migrate`,
		fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion),
	)
	return applySchemaMigration(ctx, db, statements)
}

// migrateLibraryV3ToV4 只为 Agent 补名称,保留已有布局及 Activation 的全部身份与分组依据。
func migrateLibraryV3ToV4(ctx context.Context, db *sql.DB) error {
	createSQL, err := canonicalTableSQL(ctx, "agents")
	if err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	return applySchemaMigration(ctx, db, []string{
		`CREATE TABLE agents_migrate AS SELECT code, global_path, project_path FROM agents`,
		`DROP TABLE agents`,
		createSQL,
		agentSeedSQL,
		// 先用种子提供已知 Agent 的名称,再还原旧规则;其他已存代码暂以代码作为名称。
		`INSERT INTO agents(code, name, global_path, project_path)
		 SELECT code, code, global_path, project_path FROM agents_migrate WHERE true
		 ON CONFLICT(code) DO UPDATE SET global_path = excluded.global_path, project_path = excluded.project_path`,
		`DROP TABLE agents_migrate`,
		fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion),
	})
}

func applySchemaMigration(ctx context.Context, db *sql.DB, statements []string) error {
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate library schema: %w", err)
		}
	}
	if err := validateLibrarySchema(ctx, tx); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	return nil
}

func canonicalTableSQL(ctx context.Context, name string) (string, error) {
	expectedDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return "", err
	}
	defer expectedDB.Close()
	if _, err := expectedDB.ExecContext(ctx, schemaSQL); err != nil {
		return "", err
	}
	var createSQL string
	if err := expectedDB.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&createSQL); err != nil {
		return "", err
	}
	return createSQL, nil
}
