package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"
)

const busyTimeout = 5 * time.Second

var (
	ErrNotFound      = errors.New("record not found")
	ErrAlreadyExists = errors.New("record already exists")
	ErrBusy          = errors.New("library database is busy")
	ErrUnacceptable  = errors.New("unacceptable library schema")
)

type querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type queries struct {
	q querier
}

type Store struct {
	queries
	db *sql.DB
}

type Tx struct {
	queries
}

func Open(path string) (*Store, error) {
	ctx := context.Background()
	db, err := openDB(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	if err := initialize(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	_, _ = db.Exec(`PRAGMA journal_mode = WAL`)
	return newStore(db), nil
}

func newStore(db *sql.DB) *Store {
	return &Store{queries: queries{db}, db: db}
}

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", fmt.Sprintf("%s?_txlock=immediate&_busy_timeout=%d", path, busyTimeout.Milliseconds()))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func initialize(ctx context.Context, db *sql.DB) error {
	var tableCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect library: %w", err)
	}
	if tableCount > 0 {
		if err := requireIdentity(ctx, db); err != nil {
			return fmt.Errorf("open library: %w", err)
		}
	} else {
		if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("initialize library: %w", err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA application_id = %d; PRAGMA user_version = %d`, applicationID, schemaVersion)); err != nil {
			return fmt.Errorf("identify library: %w", err)
		}
	}
	if err := seedAgents(ctx, db); err != nil {
		return fmt.Errorf("open library: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) InTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", wrap(err))
	}
	defer tx.Rollback()
	if err := fn(&Tx{queries{tx}}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", wrap(err))
	}
	return nil
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	sqliteErr, ok := errors.AsType[sqlite3.Error](err)
	if !ok {
		return err
	}
	if sqliteErr.Code == sqlite3.ErrBusy || sqliteErr.Code == sqlite3.ErrLocked {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	switch sqliteErr.ExtendedCode {
	case sqlite3.ErrConstraintUnique, sqlite3.ErrConstraintPrimaryKey:
		return fmt.Errorf("%w: %w", ErrAlreadyExists, err)
	case sqlite3.ErrConstraintForeignKey:
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}
