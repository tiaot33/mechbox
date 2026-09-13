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
	librarySchemaVersion = 2
)

//go:embed schema.sql
var schemaSQL string

// errUnacceptableSchema 只标记已确认的库校验失败,不包含本地 I/O 或取消。
var errUnacceptableSchema = errors.New("unacceptable library schema")

func validateLibrarySchema(ctx context.Context, db *sql.DB) error {
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

func schemaFingerprint(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
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
	case 1:
		return migrateLibraryV1ToV2(ctx, db)
	default:
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
}

// migrateLibraryV1ToV2 给 skill_versions 补上 git_tree_sha,并把 sqlite_master 写成与当前 schema.sql 一致。
func migrateLibraryV1ToV2(ctx context.Context, db *sql.DB) error {
	createSQL, err := canonicalTableSQL(ctx, "skill_versions")
	if err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE skill_versions_migrate (id INTEGER PRIMARY KEY, skill_id INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_versions_migrate(id, skill_id) SELECT id, skill_id FROM skill_versions`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE skill_versions`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_versions(id, skill_id, git_tree_sha) SELECT id, skill_id, '' FROM skill_versions_migrate`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE skill_versions_migrate`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion)); err != nil {
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
