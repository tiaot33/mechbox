package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	applicationID    = 1397443650
	schemaVersion    = 22
	builtinAgentsSQL = `
		INSERT INTO agents(code, name, global_path, project_path, detect_path) VALUES
			('claude-code', 'Claude Code', '.claude/skills', '.claude/skills', '.claude'),
			('codex', 'Codex', '.agents/skills', '.agents/skills', '.codex')
		ON CONFLICT(code) DO UPDATE SET name = excluded.name, global_path = excluded.global_path, project_path = excluded.project_path, detect_path = excluded.detect_path
		WHERE custom = 0 AND (name, global_path, project_path, detect_path) IS NOT (excluded.name, excluded.global_path, excluded.project_path, excluded.detect_path)
	`
)

func newToken() string {
	return rand.Text()
}

//go:embed schema.sql
var schemaSQL string

func writeBuiltinAgents(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, builtinAgentsSQL); err != nil {
		return err
	}
	if err := validateSchema(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO replica(id, token) VALUES (1, ?) ON CONFLICT(id) DO NOTHING`, newToken()); err != nil {
		return err
	}
	return tx.Commit()
}

type schemaReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateSchema(ctx context.Context, db schemaReader) error {
	if err := requireIdentity(ctx, db); err != nil {
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
		return fmt.Errorf("%w: schema fingerprint mismatch", ErrUnacceptable)
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
		return fmt.Errorf("%w: foreign key check failed", ErrUnacceptable)
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

func requireIdentity(ctx context.Context, db schemaReader) error {
	var actualID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&actualID); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	switch {
	case actualID != applicationID:
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", ErrUnacceptable, actualID, userVersion)
	case userVersion > schemaVersion:
		return fmt.Errorf("%w: %w: user_version=%d", ErrUnacceptable, ErrNewerSchema, userVersion)
	case userVersion < schemaVersion:
		return fmt.Errorf("%w: %w: user_version=%d", ErrUnacceptable, ErrOlderSchema, userVersion)
	}
	return nil
}
