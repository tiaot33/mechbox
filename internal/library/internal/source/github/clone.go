package github

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
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

func fetchGitHubClone(ctx context.Context, locator repoLocator, subpath string) (result discoveredSource, err error) {
	if fullCommit.MatchString(locator.Branch) {
		locator.Branch = strings.ToLower(locator.Branch)
	}
	root, err := os.MkdirTemp("", "skills-git-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(root)
	repository := filepath.Join(root, "repository")
	if err := validateGitRef(ctx, locator.Branch); err != nil {
		return result, err
	}
	clone := func(remote string) error {
		_, err := runSourceGit(ctx, "clone", "--quiet", "--depth", "1", "--single-branch", "--branch", locator.Branch, "--", remote, repository)
		if err != nil && ctx.Err() == nil && fullCommit.MatchString(locator.Branch) {
			if removeErr := os.RemoveAll(repository); removeErr != nil {
				return removeErr
			}
			if _, initErr := runSourceGit(ctx, "init", "--quiet", repository); initErr != nil {
				return initErr
			}
			if _, fetchErr := runSourceGit(ctx, "-C", repository, "fetch", "--quiet", "--depth", "1", "--", remote, locator.Branch); fetchErr != nil {
				return fetchErr
			}
			_, err = runSourceGit(ctx, "-C", repository, "checkout", "--quiet", "--detach", "FETCH_HEAD")
		}
		return missingRefError(err, locator.Branch)
	}
	err = clone(locator.Repository)
	if err != nil && ctx.Err() == nil {
		if remote, ok := strings.CutPrefix(locator.Repository, "https://github.com/"); ok {
			if removeErr := os.RemoveAll(repository); removeErr != nil {
				return result, removeErr
			}
			firstErr := err
			err = clone("git@github.com:" + remote)
			if err != nil && ctx.Err() == nil {
				err = preferGitHubHTTPSError(firstErr, err)
			}
		}
	}
	if err != nil {
		return result, err
	}
	output, err := runSourceGit(ctx, "-C", repository, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	fsys := os.DirFS(repository)
	current := "."
	if subpath != "" && subpath != "." {
		for component := range strings.SplitSeq(subpath, "/") {
			entries, err := fs.ReadDir(fsys, current)
			if err != nil {
				return result, err
			}
			index := slices.IndexFunc(entries, func(entry fs.DirEntry) bool { return entry.Name() == component })
			if index < 0 {
				return result, ErrSubpathNotFound
			}
			if !entries[index].IsDir() {
				return result, invalidInput("source subpath must contain only real directories")
			}
			current = path.Join(current, component)
		}
	}
	result, err = discoverSkills(ctx, fsys, current, nil)
	result.ref = strings.TrimSpace(string(output))
	if err != nil {
		return result, err
	}
	for i, skill := range result.skills {
		sha, shaErr := readGitTreeSHA(ctx, repository, skill.candidate.Path)
		if shaErr != nil {
			return discoveredSource{}, shaErr
		}
		result.skills[i].gitTreeSHA = sha
	}
	return result, nil
}

func readGitTreeSHA(ctx context.Context, repository, skillPath string) (string, error) {
	spec := "HEAD^{tree}"
	if skillPath != "" && skillPath != "." {
		spec = "HEAD:" + skillPath
	}
	output, err := runSourceGit(ctx, "-C", repository, "rev-parse", spec)
	if err != nil {
		return "", err
	}
	sha := strings.ToLower(strings.TrimSpace(string(output)))
	if !fullCommit.MatchString(sha) {
		return "", errors.New("read git tree: invalid skill directory tree SHA")
	}
	return sha, nil
}

func discoverSkills(ctx context.Context, fsys fs.FS, root string, budget *discovery.Budget) (discoveredSource, error) {
	found, diagnostics, err := discovery.ScanFS(ctx, fsys, root, budget)
	if err != nil {
		return discoveredSource{}, err
	}
	skills := make([]discoveredSkill, len(found))
	for i, skill := range found {
		skills[i] = discoveredSkill{name: skill.Name, candidate: skill.Candidate, files: skill.Files}
	}
	return discoveredSource{skills: skills, diagnostics: diagnostics}, nil
}
