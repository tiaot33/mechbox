package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type sqliteLibrary struct {
	db   *sql.DB
	home string
	mu   sync.RWMutex

	previewMu sync.Mutex
	previews  map[FetchPreviewID]*previewState
	closed    bool

	githubAuth githubAuth
	githubMemo githubObjectMemo
}

type libraryOperationKey struct{}

func (l *sqliteLibrary) sharedOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.RLock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.RUnlock
}

func (l *sqliteLibrary) exclusiveOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.Lock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.Unlock
}

const libraryBusyTimeout = 5 * time.Second

func openLibraryDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", fmt.Sprintf("%s?_txlock=immediate&_busy_timeout=%d", path, libraryBusyTimeout.Milliseconds()))
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

func Open(path string) (Library, error) {
	db, err := openLibraryDB(context.Background(), path)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	var tableCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("inspect library: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open library: find home directory: %w", err)
	}
	if tableCount > 0 {
		if err := requireLibraryIdentity(context.Background(), db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("open library: %w", err)
		}
	} else {
		if _, err := db.Exec(schemaSQL); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize library: %w", err)
		}
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA application_id = %d; PRAGMA user_version = %d`, libraryApplicationID, librarySchemaVersion)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("identify library: %w", err)
		}
	}
	if err := seedAgents(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open library: %w", err)
	}
	_, _ = db.Exec(`PRAGMA journal_mode = WAL`)
	return &sqliteLibrary{db: db, home: home, previews: map[FetchPreviewID]*previewState{}, githubMemo: newGitHubMemo()}, nil
}

func (l *sqliteLibrary) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.previewMu.Lock()
	l.closed = true
	for _, state := range l.previews {
		state.timer.Stop()
	}
	clear(l.previews)
	l.previewMu.Unlock()
	l.githubAuth.anonymousReset.Store(0)
	l.githubAuth.useToken.Store(false)
	l.githubAuth.useCLI.Store(false)
	l.githubMemo.release()
	return l.db.Close()
}
