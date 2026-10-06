package library

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

type sqliteLibrary struct {
	store *store.Store
	home  string
	mu    sync.RWMutex

	previewMu sync.Mutex
	previews  map[FetchPreviewID]*previewState
	closed    bool

	github *github.Client
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

func Open(path string) (Library, error) {
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open library: find home directory: %w", err)
	}
	return &sqliteLibrary{store: db, home: home, previews: map[FetchPreviewID]*previewState{}, github: github.NewClient()}, nil
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
	l.github.Close()
	return l.store.Close()
}
