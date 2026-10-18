package github

import (
	"context"
	"sync"

	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/source/gitcmd"
)

const (
	maxFetchSize     = 64 << 20
	fetchConcurrency = 8
)

func runConcurrent(ctx context.Context, n int, fn func(context.Context, int) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	slots := make(chan struct{}, fetchConcurrency)
	var wg sync.WaitGroup
	for i := range n {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			defer func() { <-slots }()
			if err := fn(ctx, i); err != nil {
				cancel(err)
			}
		})
	}
	wg.Wait()
	return context.Cause(ctx)
}

type discoveredSource struct {
	skills      []discoveredSkill
	diagnostics []discovery.Diagnostic
	ref         string
}

func fetchGitHubClone(ctx context.Context, locator repoLocator, subpath string) (discoveredSource, error) {
	checkout, err := gitcmd.Clone(ctx, remotes(locator.Repository), locator.Branch, subpath)
	if err != nil {
		return discoveredSource{}, err
	}
	result := discoveredSource{diagnostics: checkout.Diagnostics, ref: checkout.Commit}
	for _, skill := range checkout.Skills {
		result.skills = append(result.skills, discoveredSkill{name: skill.Name, candidate: skill.Candidate, files: skill.Files, gitTreeSHA: skill.Tree})
	}
	return result, nil
}
