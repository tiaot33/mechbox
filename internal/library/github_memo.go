package library

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
)

// githubMemoTreeLimit 是进程内 recursive tree 条数上限。
// CheckUpdates 每个 Source 最多一棵 tree;TUI 来回 Fetch 的仓库通常是个位数。
// 16 棵覆盖一个会话的工作集。每棵是解码后的目录 listing,16 棵大约一两 MB,
// 避免长时间浏览后无界占用。
const githubMemoTreeLimit = 16

// githubMemoCommitLimit 是进程内按完整 SHA 记忆的 commits 条数上限。
// 与 tree 同量级:钉住 SHA 的 Fetch / 检查是少数,16 条足够当前工作集。
const githubMemoCommitLimit = 16

// githubMemoBlobLimit 是进程内 blob 正文总字节上限。
// Agent Skills 以 SKILL.md 与小脚本为主;256 KiB 能留住当前会话预览过的
// SKILL.md 和刚安装的辅助文件。更大的条目会挤出最久未用的,再访问回源;
// 单条超过上限则不写入。
const githubMemoBlobLimit = 256 << 10

// githubMemoKind 区分三种 git 对象。LRU 三张表本就分开;在途表也按类型分 key,
// 同一 SHA 的 tree 请求与 blob 请求不会被误合并。
type githubMemoKind uint8

const (
	memoCommit githubMemoKind = iota
	memoTree
	memoBlob
)

// githubObjectMemo 是 Library 实例上的网络层 memo:GitHub 内容寻址对象只从远端取一次。
// 不是数据来源,不落库、不进入 Sync、没有 TTL;Close 时释放。
type githubObjectMemo struct {
	mu       sync.Mutex
	dead     bool
	trees    lruMemo[githubTreeResponse]
	commits  lruMemo[githubCommitMemo]
	blobs    lruMemo[[]byte]
	inFlight map[githubMemoKey]*githubFlight
}

type githubMemoKey struct {
	raw         bool // 仅隔离 raw 与 blob API 的在途请求;已验证正文仍共用同一缓存 key
	kind        githubMemoKind
	owner, repo string
	id          string
}

type githubCommitMemo struct {
	commit, tree string
}

// githubFlight 是同 key 的在途请求:后到者等待先到者的结果,远端每种对象只出一份。
// leader 是先到者的 ctx:它被取消导致的失败与等待者无关,等待者据此决定重新排队而不是沿用错误。
type githubFlight struct {
	done   chan struct{}
	leader context.Context
	value  any
	err    error
}

// lruMemo 是按访问顺序逐出的有界表。size 为 nil 时每条权重 1(按条数封顶);
// 任何条目至少计 1,空正文也占位,order 不会无界增长。clone 为 nil 时值按原样存取。
type lruMemo[V any] struct {
	order []githubMemoKey
	items map[githubMemoKey]V
	used  int
	limit int
	size  func(V) int
	clone func(V) V
}

func newGitHubMemo() githubObjectMemo {
	return githubObjectMemo{
		trees: lruMemo[githubTreeResponse]{
			limit: githubMemoTreeLimit,
			clone: func(t githubTreeResponse) githubTreeResponse { t.Tree = slices.Clone(t.Tree); return t },
		},
		commits: lruMemo[githubCommitMemo]{limit: githubMemoCommitLimit},
		blobs: lruMemo[[]byte]{
			limit: githubMemoBlobLimit,
			size:  func(b []byte) int { return len(b) },
			clone: bytes.Clone,
		},
	}
}

func (m *lruMemo[V]) weight(value V) int {
	if m.size == nil {
		return 1
	}
	return max(1, m.size(value))
}

func (m *lruMemo[V]) copy(value V) V {
	if m.clone == nil {
		return value
	}
	return m.clone(value)
}

func (m *lruMemo[V]) get(key githubMemoKey) (V, bool) {
	value, ok := m.items[key]
	if ok {
		m.touch(key)
	}
	return m.copy(value), ok
}

// put 写入一个副本。单条超过上限不写;覆盖已有 key 与新增走同一条"先腾位再放入"的路径。
func (m *lruMemo[V]) put(key githubMemoKey, value V) {
	if m.items == nil {
		m.items = map[githubMemoKey]V{}
	}
	n := m.weight(value)
	if m.limit > 0 && n > m.limit {
		return
	}
	if _, ok := m.items[key]; ok {
		m.remove(key)
	}
	for len(m.order) > 0 && m.limit > 0 && m.used+n > m.limit {
		m.remove(m.order[0])
	}
	m.items[key] = m.copy(value)
	m.used += n
	m.order = append(m.order, key)
}

func (m *lruMemo[V]) remove(key githubMemoKey) {
	value, ok := m.items[key]
	if !ok {
		return
	}
	m.used -= m.weight(value)
	delete(m.items, key)
	if i := slices.Index(m.order, key); i >= 0 {
		m.order = slices.Delete(m.order, i, i+1)
	}
}

func (m *lruMemo[V]) touch(key githubMemoKey) {
	if i := slices.Index(m.order, key); i >= 0 {
		m.order = append(slices.Delete(m.order, i, i+1), key)
	}
}

// memoKey 规范化 key:owner / repo 在 GitHub 上大小写不敏感,统一小写;
// id 为 40 位 SHA 时小写,否则是分支 / tag 名(Git 大小写敏感),原样保留。
func memoKey(kind githubMemoKind, owner, repo, id string) githubMemoKey {
	id = strings.TrimSpace(id)
	if fullCommit.MatchString(id) {
		id = strings.ToLower(id)
	}
	return githubMemoKey{kind: kind, owner: strings.ToLower(owner), repo: strings.ToLower(repo), id: id}
}

func (m *githubObjectMemo) withLock(fn func()) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return false
	}
	fn()
	return true
}

// release 清空三张表并让后续读写都 miss。在途 flight 由各自的 leader 收尾(finish 不受 dead 影响),
// 等待者不会被遗留。
func (m *githubObjectMemo) release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dead = true
	m.trees = lruMemo[githubTreeResponse]{}
	m.commits = lruMemo[githubCommitMemo]{}
	m.blobs = lruMemo[[]byte]{}
	m.inFlight = nil
}

// begin 登记一个在途请求。返回 joined 为 true 表示同 key 已有先到者,调用方应等待其 done;
// memo 已释放时返回 nil,调用方直接出网、不记。
func (m *githubObjectMemo) begin(ctx context.Context, key githubMemoKey) (flight *githubFlight, joined bool) {
	if !m.withLock(func() {
		if existing, ok := m.inFlight[key]; ok {
			flight, joined = existing, true
			return
		}
		flight = &githubFlight{done: make(chan struct{}), leader: ctx}
		if m.inFlight == nil {
			m.inFlight = map[githubMemoKey]*githubFlight{}
		}
		m.inFlight[key] = flight
	}) {
		return nil, false
	}
	return flight, joined
}

// finish 注销在途请求并唤醒等待者;memo 已释放时也要唤醒,否则等待者悬空。
func (m *githubObjectMemo) finish(key githubMemoKey, flight *githubFlight) {
	m.withLock(func() { delete(m.inFlight, key) })
	close(flight.done)
}

// memoGet 在锁内查表并返回副本;memo 已释放时恒 miss。
func memoGet[V any](m *githubObjectMemo, table *lruMemo[V], key githubMemoKey) (V, bool) {
	key.raw = false
	var value V
	var ok bool
	if !m.withLock(func() { value, ok = table.get(key) }) {
		var zero V
		return zero, false
	}
	return value, ok
}

// memoPut 在锁内写入副本;memo 已释放时丢弃。
func memoPut[V any](m *githubObjectMemo, table *lruMemo[V], key githubMemoKey, value V) {
	key.raw = false
	m.withLock(func() { table.put(key, value) })
}

// memoized 是 memo 的读路径:先查表;miss 时同 key 只让一个调用方出网(single-flight),其余等待它的结果。
// fetch 返回 (值, 可写入, 错误):成功且可写入才进表——truncated 的 tree、校验失败的 blob、
// 分支 / tag 的 commit 都不进。等待者自己的 ctx 取消立即返回;先到者因它自己的 ctx 取消而失败时,
// 等待者不沿用这个错误,而是重新排队。fetch panic 时等待者得到 errGitHubContentsFallback 而不是零值,
// panic 继续向上传播。
func memoized[V any](ctx context.Context, m *githubObjectMemo, table *lruMemo[V], key githubMemoKey, fetch func() (V, bool, error)) (V, error) {
	var zero V
	for {
		if value, ok := memoGet(m, table, key); ok {
			return value, nil
		}
		flight, joined := m.begin(ctx, key)
		if flight == nil {
			value, _, err := fetch()
			return value, err
		}
		if !joined {
			return lead(m, table, key, flight, fetch)
		}
		select {
		case <-flight.done:
		case <-ctx.Done():
			return zero, ctx.Err()
		}
		if flight.err == nil {
			return table.copy(flight.value.(V)), nil
		}
		if flight.leader.Err() != nil {
			continue
		}
		return zero, flight.err
	}
}

// lead 作为先到者出网,把结果交给等待者并按需写表。
func lead[V any](m *githubObjectMemo, table *lruMemo[V], key githubMemoKey, flight *githubFlight, fetch func() (V, bool, error)) (value V, err error) {
	completed := false
	defer func() {
		if completed {
			flight.value, flight.err = value, err
		} else {
			flight.err = errGitHubContentsFallback
		}
		m.finish(key, flight)
	}()
	var storable bool
	value, storable, err = fetch()
	completed = true
	if err == nil && storable {
		memoPut(m, table, key, value)
	}
	return value, err
}
