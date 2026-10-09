package library

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/s3"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/download"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// Library 是桌面端使用的完整进程内技能库(Library) API。它不是 HTTP 或 RPC 契约;
// 见 docs/adr/0008-desktop-is-wails-v3-in-process.md。
type Library struct {
	store *store.Store
	home  string
	mu    sync.RWMutex

	previewMu sync.Mutex
	previews  map[FetchPreviewID]*previewState
	closed    bool

	github  *github.Client
	sources source.Kinds

	syncSettings atomic.Pointer[s3.Settings]
}

const (
	maxFetchSize  = 64 << 20
	maxFetchFiles = 1000
)

type libraryOperationKey struct{}

func (l *Library) sharedOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.RLock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.RUnlock
}

func (l *Library) exclusiveOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.Lock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.Unlock
}

// Open 打开 path 处的技能库。空库会建表并写入应用标识与 schema 版本;不是 SQLite 文件、应用标识、
// schema 版本或表结构不符时返回 FailedPrecondition 且不改动文件;另一进程占着写锁超过等待上限返回 Busy。
// 应用内置的 Agent 以本版应用为准:缺的补上,已有的名称和目录被覆盖,厂商换了读取目录时已有的库也随应用版本用上新目录;
// 库里有、本版应用不认识的 Agent(例如从装了更新版本应用的机器同步来的)保持原样。
func Open(path string) (lib *Library, err error) {
	defer func() { err = typedError(err) }()
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open library: find home directory: %w", err)
	}
	client := github.NewClient()
	return &Library{
		store:    db,
		home:     home,
		previews: map[FetchPreviewID]*previewState{},
		github:   client,
		sources: source.Kinds{
			github.Type:   client,
			download.Type: download.New(maxFetchFiles, maxFetchSize),
			local.Type:    local.New(maxFetchFiles, maxFetchSize),
		},
	}, nil
}

// Close 关闭 Library:丢弃全部入库预览句柄,清空 GitHub 缓存并关闭数据库。
func (l *Library) Close() (err error) {
	defer func() { err = typedError(err) }()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.previewMu.Lock()
	l.closed = true
	for _, state := range l.previews {
		state.timer.Stop()
	}
	clear(l.previews)
	l.previewMu.Unlock()
	l.github.Close()
	return l.store.Close()
}

// SetGitHubToken 设置访问 GitHub 用的个人访问令牌,之后的入库预览、入库、检查更新与更新立即使用。
// 它只用在「用令牌访问」这一步:先匿名访问,不行再用令牌,再不行用 gh,顺序不变。
// 传空字符串表示没有传入,回到读取 GITHUB_TOKEN / GH_TOKEN 环境变量。令牌只在这个 Library 实例上有效,不写进库。
func (l *Library) SetGitHubToken(token string) {
	l.github.SetToken(token)
}

func (l *Library) applyChange(ctx context.Context, change *agentdir.Change, record func(*store.Tx) error) error {
	if err := l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := record(tx); err != nil {
			return err
		}
		return change.Commit(ctx)
	}); err != nil {
		return err
	}
	change.Done()
	return nil
}
