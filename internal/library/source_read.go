package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/download"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
)

const sourceStore SourceType = "store"

const (
	maxFetchSize     = 64 << 20
	maxFetchFiles    = 1000
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

func errUnsupportedSource() error {
	return &Error{Code: InvalidArgument, Message: "unsupported source type"}
}

func (l *sqliteLibrary) openSnapshot(ctx context.Context, src Source, subpath string, hint source.State) (source.Snapshot, error) {
	switch src.Type {
	case SourceLocal:
		locator, err := json.Marshal(src.Locator)
		if err != nil {
			return nil, fmt.Errorf("encode source locator: %w", err)
		}
		return local.Open(ctx, locator, maxFetchFiles, maxFetchSize)
	case SourceURL:
		return openDownload(ctx, src.Locator.URL, hint)
	case sourceStore:
		base := os.Getenv("SKILLS_STORE_URL")
		if base == "" {
			return nil, &Error{Code: NotConfigured, Message: "fetch store source: SKILLS_STORE_URL is not configured"}
		}
		if _, err := url.Parse(base); err != nil {
			return nil, &Error{Code: NotConfigured, Message: "fetch store source: SKILLS_STORE_URL is invalid", Err: err}
		}
		return openDownload(ctx, strings.TrimRight(base, "/")+"/"+url.PathEscape(src.Locator.PackageID)+"/"+url.PathEscape(src.Locator.Version), hint)
	case SourceGitHub:
		return l.openGitHub(ctx, src.Locator, subpath)
	default:
		return nil, errUnsupportedSource()
	}
}

func openDownload(ctx context.Context, location string, hint source.State) (source.Snapshot, error) {
	locator, err := json.Marshal(SourceLocator{URL: location})
	if err != nil {
		return nil, fmt.Errorf("encode source locator: %w", err)
	}
	return download.Open(ctx, locator, hint, maxFetchFiles, maxFetchSize)
}

func fetchGitHubClone(ctx context.Context, locator SourceLocator, subpath string) (result discoveredSource, err error) {
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
	source := os.DirFS(repository)
	current := "."
	if subpath != "" && subpath != "." {
		for component := range strings.SplitSeq(subpath, "/") {
			entries, err := fs.ReadDir(source, current)
			if err != nil {
				return result, err
			}
			index := slices.IndexFunc(entries, func(entry fs.DirEntry) bool { return entry.Name() == component })
			if index < 0 {
				return result, &Error{Code: NotFound, Message: "source subpath not found"}
			}
			if !entries[index].IsDir() {
				return result, &Error{Code: InvalidArgument, Message: "source subpath must contain only real directories"}
			}
			current = path.Join(current, component)
		}
	}
	result, err = discoverSkills(ctx, source, current, nil)
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
		return "", fetchError(Internal, "read git tree", errors.New("invalid skill directory tree SHA"))
	}
	return sha, nil
}

func discoverSkills(ctx context.Context, source fs.FS, root string, budget *discovery.Budget) (discoveredSource, error) {
	found, diagnostics, err := discovery.ScanFS(ctx, source, root, budget)
	if err != nil {
		return discoveredSource{}, err
	}
	skills := make([]discoveredSkill, len(found))
	for i, skill := range found {
		skills[i] = discoveredSkill{name: skill.Name, candidate: skill.Candidate, files: skill.Files}
	}
	return discoveredSource{skills: skills, diagnostics: diagnostics}, nil
}
