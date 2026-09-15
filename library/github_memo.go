package library

import (
	"bytes"
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
// SKILL.md 和刚安装的辅助文件。更大的条目会挤出最久未用的,再访问回源。
const githubMemoBlobLimit = 256 << 10

// githubObjectMemo 是 Library 实例上的网络层 memo:GitHub 内容寻址对象只从远端取一次。
// 不是数据来源,不落库、不进入 Sync、没有 TTL;Close 时释放。
type githubObjectMemo struct {
	mu       sync.Mutex
	dead     bool
	trees    countedMemo[githubMemoKey, githubTreeResponse]
	commits  countedMemo[githubMemoKey, githubCommitMemo]
	blobs    byteMemo
	inFlight map[githubMemoKey]*githubFlight
}

type githubMemoKey struct {
	owner, repo, kind, id string
}

type githubCommitMemo struct {
	commit, tree string
}

type githubFlight struct {
	done   chan struct{}
	tree   githubTreeResponse
	blob   []byte
	commit githubCommitMemo
	err    error
}

type countedMemo[K comparable, V any] struct {
	order []K
	items map[K]V
	limit int
}

func (m *countedMemo[K, V]) get(key K) (V, bool) {
	value, ok := m.items[key]
	if ok {
		m.touch(key)
	}
	return value, ok
}

func (m *countedMemo[K, V]) put(key K, value V) {
	if m.items == nil {
		m.items = map[K]V{}
	}
	if _, ok := m.items[key]; ok {
		m.items[key] = value
		m.touch(key)
		return
	}
	if m.limit > 0 && len(m.items) >= m.limit {
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.items, oldest)
	}
	m.items[key] = value
	m.order = append(m.order, key)
}

func (m *countedMemo[K, V]) touch(key K) {
	if i := slices.Index(m.order, key); i >= 0 {
		m.order = append(m.order[:i], m.order[i+1:]...)
		m.order = append(m.order, key)
	}
}

type byteMemo struct {
	order []githubMemoKey
	items map[githubMemoKey][]byte
	bytes int
	limit int
}

func (m *byteMemo) get(key githubMemoKey) ([]byte, bool) {
	value, ok := m.items[key]
	if ok {
		m.touch(key)
	}
	return value, ok
}

func (m *byteMemo) put(key githubMemoKey, value []byte) {
	if m.items == nil {
		m.items = map[githubMemoKey][]byte{}
	}
	if old, ok := m.items[key]; ok {
		m.bytes -= len(old)
		m.items[key] = value
		m.bytes += len(value)
		m.touch(key)
		return
	}
	if m.limit > 0 && len(value) > m.limit {
		return
	}
	for m.limit > 0 && len(m.items) > 0 && m.bytes+len(value) > m.limit {
		oldest := m.order[0]
		m.order = m.order[1:]
		m.bytes -= len(m.items[oldest])
		delete(m.items, oldest)
	}
	m.items[key] = value
	m.bytes += len(value)
	m.order = append(m.order, key)
}

func (m *byteMemo) touch(key githubMemoKey) {
	if i := slices.Index(m.order, key); i >= 0 {
		m.order = append(m.order[:i], m.order[i+1:]...)
		m.order = append(m.order, key)
	}
}

func memoKey(kind, owner, repo, id string) githubMemoKey {
	return githubMemoKey{owner: owner, repo: repo, kind: kind, id: id}
}

func (m *githubObjectMemo) release() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dead = true
	m.trees = countedMemo[githubMemoKey, githubTreeResponse]{}
	m.commits = countedMemo[githubMemoKey, githubCommitMemo]{}
	m.blobs = byteMemo{}
}

func (m *githubObjectMemo) ensureLimits() {
	if m.trees.limit == 0 {
		m.trees.limit = githubMemoTreeLimit
		m.commits.limit = githubMemoCommitLimit
		m.blobs.limit = githubMemoBlobLimit
	}
}

func (m *githubObjectMemo) begin(key githubMemoKey) (wait *githubFlight, joined bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return nil, false
	}
	if existing, ok := m.inFlight[key]; ok {
		return existing, true
	}
	wait = &githubFlight{done: make(chan struct{})}
	if m.inFlight == nil {
		m.inFlight = map[githubMemoKey]*githubFlight{}
	}
	m.inFlight[key] = wait
	return wait, false
}

func (m *githubObjectMemo) finish(key githubMemoKey, wait *githubFlight) {
	if wait == nil {
		return
	}
	if m != nil {
		m.mu.Lock()
		delete(m.inFlight, key)
		m.mu.Unlock()
	}
	close(wait.done)
}

func (m *githubObjectMemo) getTree(key githubMemoKey) (githubTreeResponse, bool) {
	if m == nil {
		return githubTreeResponse{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return githubTreeResponse{}, false
	}
	tree, ok := m.trees.get(key)
	if !ok {
		return githubTreeResponse{}, false
	}
	tree.Tree = slices.Clone(tree.Tree)
	return tree, true
}

func (m *githubObjectMemo) storeTree(key githubMemoKey, tree githubTreeResponse) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return
	}
	m.ensureLimits()
	stored := tree
	stored.Tree = slices.Clone(tree.Tree)
	m.trees.put(key, stored)
}

func (m *githubObjectMemo) getBlob(key githubMemoKey) ([]byte, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return nil, false
	}
	content, ok := m.blobs.get(key)
	if !ok {
		return nil, false
	}
	return bytes.Clone(content), true
}

func (m *githubObjectMemo) storeBlob(key githubMemoKey, content []byte) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return
	}
	m.ensureLimits()
	m.blobs.put(key, bytes.Clone(content))
}

func (m *githubObjectMemo) getCommit(key githubMemoKey) (githubCommitMemo, bool) {
	if m == nil {
		return githubCommitMemo{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return githubCommitMemo{}, false
	}
	return m.commits.get(key)
}

func (m *githubObjectMemo) storeCommit(key githubMemoKey, commit githubCommitMemo) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		return
	}
	m.ensureLimits()
	m.commits.put(key, commit)
}

func githubObjectID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}
