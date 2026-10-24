package library

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/s3"
	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/download"
	"github.com/tiaot33/mechbox/internal/library/internal/source/git"
	"github.com/tiaot33/mechbox/internal/library/internal/source/github"
	"github.com/tiaot33/mechbox/internal/library/internal/source/local"
	"github.com/tiaot33/mechbox/internal/library/internal/source/wellknown"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

// Library 是桌面端使用的完整进程内技能库(Library) API。它不是 HTTP 或 RPC 契约;
// 见 docs/adr/0008-desktop-is-wails-v3-in-process.md。
type Library struct {
	store  *store.Store
	home   agentdir.Home
	distro string
	mu     sync.RWMutex

	previewMu sync.Mutex
	previews  map[FetchPreviewID]*previewState
	plans     map[UpdatePlanID]*planState
	closed    bool

	github   *github.Client
	download download.Kind
	sources  source.Kinds

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
	home, err := agentdir.UserHome()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open library: %w", err)
	}
	client := github.NewClient()
	direct := download.New(maxFetchFiles, maxFetchSize)
	return &Library{
		store:    db,
		home:     home,
		previews: map[FetchPreviewID]*previewState{},
		plans:    map[UpdatePlanID]*planState{},
		github:   client,
		download: direct,
		sources: source.Kinds{
			github.Type:    client,
			git.Type:       git.Kind{},
			download.Type:  direct,
			wellknown.Type: wellknown.New(maxFetchFiles, maxFetchSize),
			local.Type:     local.New(maxFetchFiles, maxFetchSize),
		},
	}, nil
}

// Close 关闭 Library:丢弃全部入库预览与更新计划句柄,清空 GitHub 缓存并关闭数据库。
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
	for _, state := range l.plans {
		state.timer.Stop()
	}
	clear(l.plans)
	l.previewMu.Unlock()
	l.github.Close()
	return l.store.Close()
}

// HomeDirectory 返回各 Agent 全局目录所在的用户主目录:Open 时取当前用户的主目录,拉回同步后重新取;
// 用 [Library.SetWSLDistro] 选了 WSL 发行版时是那个发行版的主目录,形如 \\wsl.localhost\<发行版>\home\<用户>。
// [Agent].GlobalDirectory、全局作用域的 [Library.ScopeDirectory] 都在它下面,界面据此把路径缩写成 ~。
func (l *Library) HomeDirectory() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.home.Root()
}

// ListWSLDistros 列出这台机器上装的 WSL 发行版的名字,设置里据此给出各 Agent 的全局目录可以放在哪。
// 不在 Windows 上或没装 WSL 时返回空列表;Docker Desktop 自带的发行版不列出。
func (l *Library) ListWSLDistros(ctx context.Context) (distros []string, err error) {
	defer func() { err = typedError(err) }()
	distros, err = wsl.Distros(ctx)
	if distros == nil && err == nil {
		distros = []string{}
	}
	return distros, err
}

// SetWSLDistro 选择各 Agent 的全局目录在哪,之后的请求立即使用:传发行版名字时换成这个 WSL 发行版主目录下的对应目录,
// [Library.HomeDirectory] 随之变成发行版的主目录;传空字符串回到当前用户的主目录(Windows 本机)。
// 换了以后只认新目录,原来的全局目录里装的文件夹留在原处、不再管,库里记着的全局安装都按新目录巡检。
// 选择只在这个 Library 实例上有效,不写进库,也不随同步传到别的机器。
//
// 不论选的是哪里,路径在 \\wsl.localhost\ 或 \\wsl$\ 下的地方(发行版里的全局目录、登记在发行版里的项目、本地目录来源)
// 都按所在发行版的 Linux 规则读写:符号链接与可执行权限经 wsl.exe 在那个发行版里执行命令处理。
// 发行版打不开(没有这个发行版、WSL 没有响应)时返回 FailedPrecondition,原来的选择不变。
func (l *Library) SetWSLDistro(ctx context.Context, distro string) (err error) {
	defer func() { err = typedError(err) }()
	home, err := resolveHome(ctx, distro)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.home, l.distro = home, distro
	return nil
}

func resolveHome(ctx context.Context, distro string) (agentdir.Home, error) {
	if distro == "" {
		return agentdir.UserHome()
	}
	return agentdir.DistroHome(ctx, distro)
}

// SetGitHubToken 设置访问 GitHub 用的个人访问令牌,之后的入库预览、入库、检查更新与更新立即使用。
// 它只用在「用令牌访问」这一步:先匿名访问,不行再用令牌,再不行用 gh,顺序不变。
// 传空字符串表示没有传入,回到读取 GITHUB_TOKEN / GH_TOKEN 环境变量。令牌只在这个 Library 实例上有效,不写进库。
func (l *Library) SetGitHubToken(token string) {
	l.github.SetToken(token)
}
