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
	home string // Open 时的用户主目录;全局 Scope 全程按它解析,不跟随后续 $HOME 变化
	// mu 是共享 / 独占锁,不是读 / 写锁:DB 语句的串行化由单连接完成,这把锁保护的是
	// 磁盘安装项的 check-then-act 复合操作(Install* / Uninstall*、加入 / 移出组成员)、home 以及 SyncPull 的整库替换。
	// 网络往返期间不得持有它,否则一个排队的独占者会让所有新来的共享者一起等网络。
	mu sync.RWMutex

	previewMu sync.Mutex
	previews  map[FetchPreviewID]*previewState
	closed    bool

	githubAuth githubAuth       // GitHub 鉴权阶梯状态(限流窗口、token / CLI 已确认可用),仅本实例持有
	githubMemo githubObjectMemo // 进程内 GitHub 不可变对象 memo(tree / blob / 完整 SHA 的 commit),不是数据来源
}

type libraryOperationKey struct{}

// sharedOperation 以共享模式持有 mu;ctx 已带本实例标记(嵌套调用)时直接复用,不重复加锁。
func (l *sqliteLibrary) sharedOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.RLock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.RUnlock
}

// exclusiveOperation 以独占模式持有 mu,供磁盘与库联动的复合操作使用。
func (l *sqliteLibrary) exclusiveOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.Lock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.Unlock
}

// libraryBusyTimeout 是等另一进程释放写锁的上限,与驱动默认相同;显式写出只为让它可见。
const libraryBusyTimeout = 5 * time.Second

// openLibraryDB 打开一个库文件(本库、Sync 快照或拉回的临时库)的单连接:
// 所有事务以 BEGIN IMMEDIATE 开始——Library 里的事务全是写事务,IMMEDIATE 让写者之间的排队走
// busy_timeout,也避免 WAL 下"先读后升级为写"时立即失败的 SQLITE_BUSY_SNAPSHOT。
// 这两个参数只影响连接,不改文件,因此可以在身份校验之前生效。
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

// Open 打开已有 Library 或在 path 初始化新库。已有库须是当前 schema 版本,其他版本返回错误且不改文件。
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
	// 到这里才确认这是本工具的库。journal mode 持久化在文件头里,放在校验之前会改写外来文件。
	// WAL 下读者与写者互不阻塞(见 ADR 0012)。切换失败(文件系统不支持共享内存、另一进程的旧版本
	// 二进制正持有连接)就保持现状:busy_timeout 与 Busy 码仍然成立,下次 Open 再试。
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
