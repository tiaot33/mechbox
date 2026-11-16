package catalog

import (
	"context"

	"github.com/tiaot33/mechbox/internal/library/internal/process"
	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
)

func (c *Catalog) context(ctx context.Context) context.Context {
	if c.environ == nil {
		return ctx
	}
	return process.WithEnv(ctx, c.environ())
}

func (c *Catalog) wrap(snapshot source.Snapshot) source.Snapshot {
	if snapshot == nil || c.environ == nil {
		return snapshot
	}
	return withEnv{snapshot: snapshot, catalog: c}
}

type withEnv struct {
	snapshot source.Snapshot
	catalog  *Catalog
}

func (s withEnv) Revision() string { return s.snapshot.Revision() }

func (s withEnv) Candidates(ctx context.Context) ([]discovery.Candidate, []discovery.Diagnostic, error) {
	return s.snapshot.Candidates(s.catalog.context(ctx))
}

func (s withEnv) Names(ctx context.Context) ([]string, error) {
	return s.snapshot.Names(s.catalog.context(ctx))
}

func (s withEnv) Fetch(ctx context.Context, names []string) ([]source.Skill, error) {
	return s.snapshot.Fetch(s.catalog.context(ctx), names)
}

func (s withEnv) Check(ctx context.Context, have source.Have) (source.State, bool, error) {
	return s.snapshot.Check(s.catalog.context(ctx), have)
}

func (s withEnv) Commits(ctx context.Context, since string) ([]source.Commit, error) {
	return s.snapshot.Commits(s.catalog.context(ctx), since)
}
