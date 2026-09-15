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
// SKILL.md 和刚安装的辅助文件。更大的条目会挤出更早的,再访问回源。
const githubMemoBlobLimit = 256 << 10

// githubObjectMemo 是 Library 实例上的网络层 memo:GitHub 内容寻址对象只从远端取一次。
// 不是数据来源,不落库、不进入 Sync、没有 TTL;Close 时释放。
type githubObjectMemo struct {
	mu      sync.Mutex
	dead    bool
	trees   fifoMemo[githubTreeResponse]
	commits fifoMemo[githubCommitMemo]
	blobs   fifoMemo[[]byte]
}

type githubMemoKey struct {
	owner, repo, id string
}

type githubCommitMemo struct {
	commit, tree string
}

// fifoMemo 是按插入顺序逐出的有界表;size 为 nil 时每条权重 1(按条数封顶)。
type fifoMemo[V any] struct {
	order []githubMemoKey
	items map[githubMemoKey]V
	used  int
	limit int
	size  func(V) int
}

func newGitHubMemo() githubObjectMemo {
	return githubObjectMemo{
		trees:   fifoMemo[githubTreeResponse]{limit: githubMemoTreeLimit},
		commits: fifoMemo[githubCommitMemo]{limit: githubMemoCommitLimit},
		blobs:   fifoMemo[[]byte]{limit: githubMemoBlobLimit, size: func(b []byte) int { return len(b) }},
	}
}

func (m *fifoMemo[V]) weight(value V) int {
	if m.size == nil {
		return 1
	}
	return m.size(value)
}

func (m *fifoMemo[V]) get(key githubMemoKey) (V, bool) {
	value, ok := m.items[key]
	return value, ok
}

func (m *fifoMemo[V]) put(key githubMemoKey, value V) {
	if m.items == nil {
		m.items = map[githubMemoKey]V{}
	}
	n := m.weight(value)
	if old, ok := m.items[key]; ok {
		m.used -= m.weight(old)
		m.items[key] = value
		m.used += n
		return
	}
	for len(m.items) > 0 && m.limit > 0 && m.used+n > m.limit {
		oldest := m.order[0]
		m.order = m.order[1:]
		m.used -= m.weight(m.items[oldest])
		delete(m.items, oldest)
	}
	m.items[key] = value
	m.used += n
	m.order = append(m.order, key)
}

func memoKey(owner, repo, id string) githubMemoKey {
	return githubMemoKey{owner: owner, repo: repo, id: githubObjectID(id)}
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

func (m *githubObjectMemo) release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dead = true
	m.trees = fifoMemo[githubTreeResponse]{}
	m.commits = fifoMemo[githubCommitMemo]{}
	m.blobs = fifoMemo[[]byte]{}
}

func (m *githubObjectMemo) getTree(key githubMemoKey) (githubTreeResponse, bool) {
	var tree githubTreeResponse
	var ok bool
	if !m.withLock(func() {
		tree, ok = m.trees.get(key)
		if ok {
			tree.Tree = slices.Clone(tree.Tree)
		}
	}) {
		return githubTreeResponse{}, false
	}
	return tree, ok
}

func (m *githubObjectMemo) storeTree(key githubMemoKey, tree githubTreeResponse) {
	m.withLock(func() {
		stored := tree
		stored.Tree = slices.Clone(tree.Tree)
		m.trees.put(key, stored)
	})
}

func (m *githubObjectMemo) getBlob(key githubMemoKey) ([]byte, bool) {
	var content []byte
	var ok bool
	if !m.withLock(func() {
		content, ok = m.blobs.get(key)
		if ok {
			content = bytes.Clone(content)
		}
	}) {
		return nil, false
	}
	return content, ok
}

func (m *githubObjectMemo) storeBlob(key githubMemoKey, content []byte) {
	m.withLock(func() { m.blobs.put(key, bytes.Clone(content)) })
}

func (m *githubObjectMemo) getCommit(key githubMemoKey) (githubCommitMemo, bool) {
	var commit githubCommitMemo
	var ok bool
	if !m.withLock(func() { commit, ok = m.commits.get(key) }) {
		return githubCommitMemo{}, false
	}
	return commit, ok
}

func (m *githubObjectMemo) storeCommit(key githubMemoKey, commit githubCommitMemo) {
	m.withLock(func() { m.commits.put(key, commit) })
}

func githubObjectID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}
