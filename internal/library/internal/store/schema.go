package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/tiaot33/mechbox/internal/library/internal/store/migrations"
)

const (
	applicationID = 1397443650
	oldestVersion = 27
	baselineFile  = "testdata/00027_schema.sql"
)

var enabledInNewLibrary = []string{"claude-code", "codex"}

func newToken() string {
	return rand.Text()
}

//go:embed schema.sql
var schemaSQL string

type layout struct {
	schema string
	steps  []migrations.Step
}

func (l layout) version() int {
	return oldestVersion + len(l.steps)
}

var currentLayout = sync.OnceValues(func() (layout, error) {
	steps, err := migrations.Load(oldestVersion)
	if err != nil {
		return layout{}, fmt.Errorf("load schema migrations: %w", err)
	}
	return layout{schema: schemaSQL, steps: steps}, nil
})

func (l layout) validator() func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		return validateSchema(ctx, tx, l)
	}
}

func writeCatalogAgents(ctx context.Context, db *sql.DB, l layout, fresh bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, agent := range catalog {
		enabled := fresh && slices.Contains(enabledInNewLibrary, agent.Code)
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents(code, name, global_paths, project_paths, detect_paths, same_name, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(code) DO NOTHING`,
			agent.Code, agent.Name, jsonList(agent.GlobalPaths), jsonList(agent.ProjectPaths), jsonList(agent.DetectPaths), agent.SameName, enabled); err != nil {
			return err
		}
	}
	if err := validateSchema(ctx, tx, l); err != nil {
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

func validateSchema(ctx context.Context, db schemaReader, l layout) error {
	version, err := requireIdentity(ctx, db, l)
	if err != nil {
		return err
	}
	if version != l.version() {
		return fmt.Errorf("%w: %w: user_version=%d", ErrUnacceptable, ErrOlderSchema, version)
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
	if _, err := expectedDB.ExecContext(ctx, l.schema); err != nil {
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
		_, _ = fmt.Fprintf(hash, "%s\n%s\n", name, normalizeStatement(name, statement))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeStatement(name, statement string) string {
	normalized := strings.Join(strings.Fields(statement), " ")
	if rest, ok := strings.CutPrefix(normalized, `CREATE TABLE "`+name+`"`); ok {
		return "CREATE TABLE " + name + rest
	}
	return normalized
}

func requireIdentity(ctx context.Context, db schemaReader, l layout) (int, error) {
	var actualID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&actualID); err != nil {
		return 0, err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return 0, err
	}
	switch {
	case actualID != applicationID:
		return 0, fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", ErrUnacceptable, actualID, userVersion)
	case userVersion > l.version():
		return 0, fmt.Errorf("%w: %w: user_version=%d", ErrUnacceptable, ErrNewerSchema, userVersion)
	case userVersion < oldestVersion:
		return 0, fmt.Errorf("%w: %w: user_version=%d", ErrUnacceptable, ErrOlderSchema, userVersion)
	}
	return userVersion, nil
}
