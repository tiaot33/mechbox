package library

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
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
	githubRepoSize int64
	skills         []discoveredSkill
	diagnostics    []discovery.Diagnostic
	ref            string
	httpValidator  string
	unchanged      bool
}

func (l *sqliteLibrary) fetchSource(ctx context.Context, source Source, subpath, validator string) (discoveredSource, error) {
	switch source.Type {
	case SourceGitHub:
		return l.fetchGitHub(ctx, source.Locator, subpath)
	case SourceURL:
		return fetchArchive(ctx, source.Locator.URL, validator)
	case sourceStore:
		base := os.Getenv("SKILLS_STORE_URL")
		if base == "" {
			return discoveredSource{}, &Error{Code: NotConfigured, Message: "fetch store source: SKILLS_STORE_URL is not configured"}
		}
		if _, err := url.Parse(base); err != nil {
			return discoveredSource{}, &Error{Code: NotConfigured, Message: "fetch store source: SKILLS_STORE_URL is invalid", Err: err}
		}
		return fetchArchive(ctx, strings.TrimRight(base, "/")+"/"+url.PathEscape(source.Locator.PackageID)+"/"+url.PathEscape(source.Locator.Version), validator)
	default:
		return discoveredSource{}, &Error{Code: InvalidArgument, Message: "unsupported source type"}
	}
}

func openSnapshot(ctx context.Context, src Source, _ source.State) (source.Snapshot, error) {
	if src.Type != SourceLocal {
		return nil, nil
	}
	locator, err := json.Marshal(src.Locator)
	if err != nil {
		return nil, fmt.Errorf("encode source locator: %w", err)
	}
	return local.Open(ctx, locator, maxFetchFiles, maxFetchSize)
}

func snapshotSource(ctx context.Context, snapshot source.Snapshot) (discoveredSource, error) {
	candidates, diagnostics, err := snapshot.Candidates(ctx)
	if err != nil {
		return discoveredSource{}, err
	}
	skills := make([]discoveredSkill, len(candidates))
	for i, candidate := range candidates {
		skills[i] = discoveredSkill{name: candidate.Name, candidate: candidate}
	}
	return discoveredSource{skills: skills, diagnostics: diagnostics, ref: snapshot.Revision()}, nil
}

func snapshotSkills(ctx context.Context, snapshot source.Snapshot, selected []discoveredSkill) ([]discoveredSkill, error) {
	names := make([]string, len(selected))
	for i, skill := range selected {
		names[i] = skill.name
	}
	fetched, err := snapshot.Fetch(ctx, names)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]source.Skill, len(fetched))
	for _, skill := range fetched {
		byName[skill.Name] = skill
	}
	skills := slices.Clone(selected)
	for i := range skills {
		got := byName[skills[i].name]
		skills[i].files, skills[i].state = got.Files, got.State
	}
	return skills, nil
}

func (l *sqliteLibrary) fetchGitHub(ctx context.Context, locator SourceLocator, subpath string) (discoveredSource, error) {
	var apiErr error
	if owner, repo, ok := githubOwnerRepo(locator.Repository); ok {
		result, err := l.fetchGitHubContents(ctx, owner, repo, locator, subpath)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return discoveredSource{}, err
		}
		if !githubContentsUnavailable(err) {
			return discoveredSource{}, err
		}
		apiErr = err
	}
	result, err := fetchGitHubClone(ctx, locator, subpath)
	if err != nil {
		return result, githubCloneError(ctx, apiErr, err)
	}
	return finishDiscovery(result)
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

func responseValidator(header http.Header) string {
	if etag := header.Get("ETag"); etag != "" {
		return etag
	}
	if modified := header.Get("Last-Modified"); modified != "" {
		if _, err := http.ParseTime(modified); err == nil {
			return modified
		}
	}
	return ""
}

func applyValidator(req *http.Request, validator string) {
	if validator == "" {
		return
	}
	if _, err := http.ParseTime(validator); err == nil {
		req.Header.Set("If-Modified-Since", validator)
		return
	}
	req.Header.Set("If-None-Match", validator)
}

func fetchArchive(ctx context.Context, location, validator string) (result discoveredSource, err error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return result, argumentError("fetch source", "url", "invalid source URL")
	}
	applyValidator(req, validator)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fetchError(fetchErrorCode(err), "fetch source", errors.New("source could not be reached"))
	}
	defer response.Body.Close()
	if validator != "" && response.StatusCode == http.StatusNotModified {
		return discoveredSource{httpValidator: validator, unchanged: true}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := SourceUnreachable
		if response.StatusCode == 401 || response.StatusCode == 403 {
			code = Permission
		}
		return result, fetchError(code, "fetch source", fmt.Errorf("HTTP %d", response.StatusCode))
	}
	content, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, maxFetchSize+1))
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fetchError(SourceUnreachable, "read source", errors.New("source response could not be read"))
	}
	if len(content) > maxFetchSize {
		return result, fetchError(SourceInvalid, "read source", errors.New("archive exceeds size limit"))
	}
	result, err = readArchive(ctx, content)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(content)
	result.ref = hex.EncodeToString(sum[:])
	result.httpValidator = responseValidator(response.Header)
	return result, nil
}

func readArchive(ctx context.Context, content []byte) (discoveredSource, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return discoveredSource{}, fetchError(SourceInvalid, "read source archive", err)
	}
	if len(reader.File) > maxFetchFiles {
		return discoveredSource{}, fetchError(SourceInvalid, "read source archive", errors.New("archive exceeds file count limit"))
	}
	seen := make(map[string]bool)
	remaining := uint64(maxFetchSize)
	for _, archived := range reader.File {
		if err := ctx.Err(); err != nil {
			return discoveredSource{}, err
		}
		name := strings.TrimSuffix(archived.Name, "/")
		if name == "" || name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) || seen[name] {
			return discoveredSource{}, fetchError(SourceInvalid, "read source archive", errors.New("unsafe or duplicate path"))
		}
		seen[name] = true
		if archived.UncompressedSize64 > remaining {
			return discoveredSource{}, fetchError(SourceInvalid, "read source archive", errors.New("expanded content exceeds size limit"))
		}
		remaining -= archived.UncompressedSize64
	}
	result, err := discoverSkills(ctx, reader, ".", nil)
	if err != nil {
		return result, err
	}
	return finishDiscovery(result)
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

func finishDiscovery(result discoveredSource) (discoveredSource, error) {
	if len(result.skills) == 0 {
		return discoveredSource{}, discovery.NoCandidates(result.diagnostics)
	}
	return result, nil
}
