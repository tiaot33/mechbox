package github

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
)

const githubMemoTreeLimit = 16

const githubMemoCommitLimit = 16

const githubMemoBlobLimit = 256 << 10

type githubMemoKind uint8

const (
	memoCommit githubMemoKind = iota
	memoTree
	memoBlob
)

type githubObjectMemo struct {
	mu       sync.Mutex
	dead     bool
	trees    lruMemo[githubTreeResponse]
	commits  lruMemo[githubCommitMemo]
	blobs    lruMemo[[]byte]
	inFlight map[githubMemoKey]*githubFlight
}

type githubMemoKey struct {
	kind        githubMemoKind
	owner, repo string
	id          string
}

type githubCommitMemo struct {
	commit, tree string
}

type githubFlight struct {
	done   chan struct{}
	leader context.Context
	value  any
	err    error
}

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

func (m *githubObjectMemo) release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dead = true
	m.trees = lruMemo[githubTreeResponse]{}
	m.commits = lruMemo[githubCommitMemo]{}
	m.blobs = lruMemo[[]byte]{}
	m.inFlight = nil
}

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

func (m *githubObjectMemo) finish(key githubMemoKey, flight *githubFlight) {
	m.withLock(func() { delete(m.inFlight, key) })
	close(flight.done)
}

func memoGet[V any](m *githubObjectMemo, table *lruMemo[V], key githubMemoKey) (V, bool) {
	var value V
	var ok bool
	if !m.withLock(func() { value, ok = table.get(key) }) {
		var zero V
		return zero, false
	}
	return value, ok
}

func memoPut[V any](m *githubObjectMemo, table *lruMemo[V], key githubMemoKey, value V) {
	m.withLock(func() { table.put(key, value) })
}

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
