package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

type sqliteLibrary struct {
	db   *sql.DB
	home string // Open 时的用户主目录;全局 Scope 全程按它解析,不跟随后续 $HOME 变化
	mu   sync.RWMutex

	fetchMu sync.Mutex
	fetches map[FetchID]*fetchSession
	closed  bool

	githubAuth githubAuth       // GitHub 鉴权阶梯状态(限流窗口、token / CLI 已确认可用),仅本实例持有
	githubMemo githubObjectMemo // 进程内 GitHub 不可变对象 memo(tree / blob / 完整 SHA 的 commit),不是数据来源
}

type libraryOperationKey struct{}

func (l *sqliteLibrary) readOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.RLock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.RUnlock
}

func (l *sqliteLibrary) writeOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.Lock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.Unlock
}

// Open 打开已有 Library 或在 path 初始化新库。已有 v1–v4 库会迁移到当前 schema。
func Open(path string) (Library, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure library: %w", err)
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
		if err := migrateLibrary(context.Background(), db, home); err != nil {
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
	return &sqliteLibrary{db: db, home: home, fetches: map[FetchID]*fetchSession{}, githubMemo: newGitHubMemo()}, nil
}

func (l *sqliteLibrary) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fetchMu.Lock()
	l.closed = true
	for _, session := range l.fetches {
		session.timer.Stop()
	}
	clear(l.fetches)
	l.fetchMu.Unlock()
	l.githubAuth.anonymousReset.Store(0)
	l.githubAuth.useToken.Store(false)
	l.githubAuth.useCLI.Store(false)
	l.githubMemo.release()
	return l.db.Close()
}
