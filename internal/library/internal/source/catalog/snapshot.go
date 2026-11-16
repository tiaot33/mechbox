package catalog

import (
	"context"

	"github.com/tiaot33/mechbox/internal/library/internal/process"
	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
)

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
	return s.snapshot.Candidates(process.WithEnv(ctx, s.catalog.environ))
}

func (s withEnv) Names(ctx context.Context) ([]string, error) {
	return s.snapshot.Names(process.WithEnv(ctx, s.catalog.environ))
}

func (s withEnv) Fetch(ctx context.Context, names []string) ([]source.Skill, error) {
	return s.snapshot.Fetch(process.WithEnv(ctx, s.catalog.environ), names)
}

func (s withEnv) Check(ctx context.Context, have source.Have) (source.State, bool, error) {
	return s.snapshot.Check(process.WithEnv(ctx, s.catalog.environ), have)
}

func (s withEnv) Commits(ctx context.Context, since string) ([]source.Commit, error) {
	return s.snapshot.Commits(process.WithEnv(ctx, s.catalog.environ), since)
}
