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
	librarySchemaVersion = 9
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

// schemaReader 让 Open/Sync 的连接校验与补种子事务内的校验共用同一套规则。
type schemaReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// errUnacceptableSchema 只标记已确认的库校验失败,不包含本地 I/O 或取消。
var errUnacceptableSchema = errors.New("unacceptable library schema")

func validateLibrarySchema(ctx context.Context, db schemaReader) error {
	if err := requireLibraryIdentity(ctx, db); err != nil {
		return err
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

// requireLibraryIdentity 在补种子之前确认已有库是本工具的当前版本;身份或版本不符则拒绝,不改文件。
// 产品尚未发布,不迁移旧版本库。
func requireLibraryIdentity(ctx context.Context, db schemaReader) error {
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
	return nil
}
