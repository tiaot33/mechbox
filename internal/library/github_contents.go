package library

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

const githubAPIHost = "api.github.com"

var errGitHubContentsFallback = errors.New("github contents api unavailable")

type githubBlob struct {
	path     string
	repoPath string
	sha      string
	size     int64
	mode     FileMode
	content  []byte
}

type githubHTTPError struct {
	status      int
	rateLimited bool
	reset       int64
}

func (e *githubHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d", e.status)
}

type githubAuth struct {
	anonymousReset atomic.Int64
	useToken       atomic.Bool
	useCLI         atomic.Bool
}

type githubClient struct {
	token    string
	useToken atomic.Bool
	useCLI   atomic.Bool
	auth     *githubAuth
	memo     *githubObjectMemo
}

func (l *sqliteLibrary) newGitHubClient() *githubClient {
	client := &githubClient{token: githubToken(), auth: &l.githubAuth, memo: &l.githubMemo}
	client.useToken.Store(l.githubAuth.useToken.Load())
	client.useCLI.Store(l.githubAuth.useCLI.Load())
	return client
}

type githubCommitResponse struct {
	SHA    string `json:"sha"`
	Commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	} `json:"commit"`
}

type githubTreeResponse struct {
	Truncated bool              `json:"truncated"`
	Tree      []githubTreeEntry `json:"tree"`
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size *int64 `json:"size"`
}

type githubBlobResponse struct {
	Encoding  string `json:"encoding"`
	Content   string `json:"content"`
	Size      int    `json:"size"`
	Truncated bool   `json:"truncated"`
}

func githubOwnerRepo(repository string) (owner, repo string, ok bool) {
	rest, ok := strings.CutPrefix(repository, "https://github.com/")
	if !ok {
		return "", "", false
	}
	rest = strings.TrimSuffix(rest, ".git")
	owner, repo, ok = strings.Cut(rest, "/")
	return owner, repo, ok && owner != "" && repo != "" && !strings.Contains(repo, "/")
}

func githubToken() string {
	return cmp.Or(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
}

func (l *sqliteLibrary) fetchGitHubContents(ctx context.Context, owner, repo string, locator SourceLocator, subpath string) (discoveredSource, error) {
	snap, err := l.fetchGitHubCommitSnapshot(ctx, owner, repo, locator)
	if err != nil {
		return discoveredSource{}, err
	}
	if err := snap.loadTree(ctx); err != nil {
		return discoveredSource{}, err
	}
	root, err := resolveGitHubSubpath(snap.entries, subpath)
	if err != nil {
		return discoveredSource{}, err
	}
	result, err := discoverFromGitHubTree(ctx, &snap, root)
	if err != nil {
		return discoveredSource{}, err
	}
	result.ref = snap.ref
	result.githubRepoSize = githubRepoSize(snap.entries)
	return finishDiscovery(result)
}

func (c *githubClient) commit(ctx context.Context, owner, repo, ref string) (commit, tree string, err error) {
	immutable := fullCommit.MatchString(ref)
	got, err := memoized(ctx, c.memo, &c.memo.commits, memoKey(memoCommit, owner, repo, ref), func() (githubCommitMemo, bool, error) {
		var payload githubCommitResponse
		if err := c.get(ctx, githubAPIURL("/repos/%s/%s/commits/%s", owner, repo, ref), &payload); err != nil {
			return githubCommitMemo{}, false, err
		}
		resolved := githubCommitMemo{
			commit: strings.ToLower(strings.TrimSpace(payload.SHA)),
			tree:   strings.ToLower(strings.TrimSpace(payload.Commit.Tree.SHA)),
		}
		if !fullCommit.MatchString(resolved.commit) || !fullCommit.MatchString(resolved.tree) {
			return githubCommitMemo{}, false, errGitHubContentsFallback
		}
		return resolved, immutable, nil
	})
	return got.commit, got.tree, err
}

func (c *githubClient) recursiveTree(ctx context.Context, owner, repo, sha string) (githubTreeResponse, error) {
	return memoized(ctx, c.memo, &c.memo.trees, memoKey(memoTree, owner, repo, sha), func() (githubTreeResponse, bool, error) {
		var payload githubTreeResponse
		err := c.get(ctx, githubAPIURL("/repos/%s/%s/git/trees/%s", owner, repo, sha)+"?recursive=1", &payload)
		return payload, err == nil && !payload.Truncated, err
	})
}

func (c *githubClient) blob(ctx context.Context, owner, repo, sha string) ([]byte, error) {
	key := memoKey(memoBlob, owner, repo, sha)
	return memoized(ctx, c.memo, &c.memo.blobs, key, func() ([]byte, bool, error) {
		var payload githubBlobResponse
		if err := c.get(ctx, githubAPIURL("/repos/%s/%s/git/blobs/%s", owner, repo, sha), &payload); err != nil {
			return nil, false, err
		}
		if payload.Truncated || !strings.EqualFold(payload.Encoding, "base64") {
			return nil, false, errGitHubContentsFallback
		}
		content, err := base64.StdEncoding.DecodeString(payload.Content)
		if err != nil || len(content) != payload.Size || gitBlobSHA(content) != key.id {
			return nil, false, errGitHubContentsFallback
		}
		return content, true, nil
	})
}

func githubAPIURL(pattern string, owner, repo, object string) string {
	return "https://" + githubAPIHost + fmt.Sprintf(pattern, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(object))
}

func (c *githubClient) get(ctx context.Context, location string, dest any) error {
	return c.request(ctx, func(token string) error { return githubAPIGetOnce(ctx, location, token, dest) }, func() error { return githubCLIGet(ctx, location, dest) })
}

func (c *githubClient) request(ctx context.Context, httpGet func(string) error, cliGet func() error) error {
	if c.useCLI.Load() {
		return cliGet()
	}
	err := errGitHubContentsFallback
	anonymousDenied := false
	if !c.useToken.Load() && time.Now().Unix() >= c.auth.anonymousReset.Load() {
		err = httpGet("")
		if httpErr, ok := errors.AsType[*githubHTTPError](err); ok && httpErr.rateLimited {
			c.auth.anonymousReset.Store(httpErr.reset)
		}
		if err == nil || ctx.Err() != nil || !isGitHubAuthRetry(err) {
			return err
		}
		anonymousDenied = githubAuthDenied(err)
	}
	httpDenied := anonymousDenied
	if c.token != "" {
		c.useToken.Store(true)
		err = httpGet(c.token)
		if err == nil {
			if anonymousDenied {
				c.auth.useToken.Store(true)
			}
			return nil
		}
		if ctx.Err() != nil || !isGitHubAuthRetry(err) {
			return err
		}
		httpDenied = githubAuthDenied(err)
	}
	if cliErr := cliGet(); cliErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	c.useCLI.Store(true)
	if anonymousDenied {
		c.auth.useToken.Store(true)
	}
	if httpDenied {
		c.auth.useCLI.Store(true)
	}
	return nil
}

func githubAuthDenied(err error) bool {
	httpErr, ok := errors.AsType[*githubHTTPError](err)
	return ok && !httpErr.rateLimited && (httpErr.status == http.StatusUnauthorized || httpErr.status == http.StatusNotFound)
}

func githubCLIGet(ctx context.Context, location string, dest any) error {
	endpoint := strings.TrimPrefix(location, "https://"+githubAPIHost)
	var body bytes.Buffer
	if err := githubCLIOutput(ctx, &body, "api", endpoint, "--method", "GET", "--hostname", "github.com"); err != nil {
		return err
	}
	if json.Unmarshal(body.Bytes(), dest) != nil {
		return errGitHubContentsFallback
	}
	return nil
}

func githubContentsUnavailable(err error) bool {
	if errors.Is(err, errGitHubContentsFallback) {
		return true
	}
	_, ok := errors.AsType[*githubHTTPError](err)
	return ok
}

func githubMaterializeError(err error) error {
	if httpErr, ok := errors.AsType[*githubHTTPError](err); ok && !httpErr.rateLimited && (httpErr.status == http.StatusUnauthorized || httpErr.status == http.StatusForbidden) {
		return fetchError(Permission, "fetch source", errors.New("authentication failed"))
	}
	return fetchError(SourceUnreachable, "fetch source", errors.New("source could not be reached"))
}

func isGitHubAuthRetry(err error) bool {
	httpErr, ok := errors.AsType[*githubHTTPError](err)
	return ok && (httpErr.status == http.StatusUnauthorized || httpErr.status == http.StatusForbidden || httpErr.status == http.StatusNotFound || httpErr.rateLimited)
}

func githubAPIGetOnce(ctx context.Context, location, token string, dest any) error {
	var body bytes.Buffer
	if err := githubDownload(ctx, location, token, &body); err != nil {
		return err
	}
	if json.Unmarshal(body.Bytes(), dest) != nil {
		return errGitHubContentsFallback
	}
	return nil
}

func githubRateLimited(response *http.Response) bool {
	if response.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0"
}

func resolveGitHubSubpath(entries []githubTreeEntry, subpath string) (string, error) {
	if subpath == "" || subpath == "." {
		return ".", nil
	}
	current := ""
	for component := range strings.SplitSeq(subpath, "/") {
		current = path.Join(current, component)
		index := slices.IndexFunc(entries, func(entry githubTreeEntry) bool { return entry.Path == current })
		if index < 0 {
			return "", &Error{Code: NotFound, Message: "source subpath not found"}
		}
		if entries[index].Type != "tree" {
			return "", &Error{Code: InvalidArgument, Message: "source subpath must contain only real directories"}
		}
	}
	return subpath, nil
}

type githubSourceView struct {
	snap *githubSourceSnapshot
	dirs map[string][]sourceEntry
}

func newGitHubSourceView(snap *githubSourceSnapshot) githubSourceView {
	v := githubSourceView{snap: snap, dirs: map[string][]sourceEntry{".": {}}}
	for _, entry := range snap.entries {
		var mode fs.FileMode
		if entry.Type == "tree" {
			mode = fs.ModeDir
			if _, exists := v.dirs[entry.Path]; !exists {
				v.dirs[entry.Path] = nil
			}
		} else if !githubRegularBlob(entry) {
			mode = fs.ModeIrregular
		}
		parent := path.Dir(entry.Path)
		v.dirs[parent] = append(v.dirs[parent], sourceEntry{path.Base(entry.Path), mode})
	}
	for dir := range v.dirs {
		slices.SortFunc(v.dirs[dir], func(a, b sourceEntry) int { return cmp.Compare(a.name, b.name) })
	}
	return v
}

func (v githubSourceView) readDir(dir string) ([]sourceEntry, error) {
	entries, ok := v.dirs[dir]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return entries, nil
}

func (v githubSourceView) readFile(ctx context.Context, name string) ([]byte, error) {
	entry, ok := v.snap.byPath[name]
	if !ok || !githubRegularBlob(entry) {
		return nil, fs.ErrNotExist
	}
	contents, err := v.snap.client.transferBlobs(ctx, v.snap.owner, v.snap.repo, v.snap.ref, 0, []githubBlob{{repoPath: entry.Path, sha: entry.SHA, content: v.snap.storedBlobs[entry.SHA]}})
	if err != nil {
		return nil, err
	}
	return contents[0], nil
}

func (v githubSourceView) readFiles(ctx context.Context, names []string) ([][]byte, []error) {
	blobs := make([]githubBlob, len(names))
	for i, name := range names {
		entry := v.snap.byPath[name]
		blobs[i] = githubBlob{repoPath: name, sha: entry.SHA, content: v.snap.storedBlobs[entry.SHA]}
	}
	contents, err := v.snap.client.transferBlobs(ctx, v.snap.owner, v.snap.repo, v.snap.ref, 0, blobs)
	errs := make([]error, len(names))
	if err != nil {
		contents = make([][]byte, len(names))
		for i := range errs {
			errs[i] = err
		}
	}
	return contents, errs
}

func discoverFromGitHubTree(ctx context.Context, snap *githubSourceSnapshot, root string) (discoveredSource, error) {
	return discoverCandidates(ctx, newGitHubSourceView(snap), root, func(skill *discoveredSkill, filename string, content []byte) error {
		blobs, treeSHA, err := githubSkillBlobs(snap.entries, skill.candidate.Path, snap.rootTree, snap.byPath)
		if err != nil {
			return err
		}
		for i, blob := range blobs {
			if blob.path == filename {
				blobs[i].content = content
				break
			}
		}
		skill.blobs, skill.gitTreeSHA = blobs, treeSHA
		return nil
	})
}

func githubUnderRoot(p, root string) bool {
	if root == "." {
		return true
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

func githubRegularBlob(entry githubTreeEntry) bool {
	return entry.Type == "blob" && (entry.Mode == "100644" || entry.Mode == "100755")
}

func githubSkillBlobs(entries []githubTreeEntry, skillPath, rootTree string, byPath map[string]githubTreeEntry) ([]githubBlob, string, error) {
	treeSHA, err := githubDirTreeSHA(skillPath, rootTree, byPath)
	if err != nil {
		return nil, "", err
	}
	var blobs []githubBlob
	for _, entry := range entries {
		if entry.Type == "tree" {
			continue
		}
		rel, ok := githubSkillRelative(entry.Path, skillPath)
		if !ok || githubDotGitPath(rel) {
			continue
		}
		if !githubRegularBlob(entry) {
			return nil, "", errGitHubContentsFallback
		}
		mode := ModeRegular
		if entry.Mode == "100755" {
			mode = ModeExecutable
		}
		blobs = append(blobs, githubBlob{path: rel, repoPath: entry.Path, sha: entry.SHA, mode: mode, size: githubEntrySize(entry)})
	}
	slices.SortFunc(blobs, func(a, b githubBlob) int { return cmp.Compare(a.path, b.path) })
	return blobs, treeSHA, nil
}

func githubSkillRelative(filePath, skillPath string) (string, bool) {
	if skillPath == "" || skillPath == "." {
		return filePath, filePath != "" && filePath != "."
	}
	if filePath == skillPath {
		return "", false
	}
	rel, ok := strings.CutPrefix(filePath, skillPath+"/")
	return rel, ok && rel != ""
}

func githubDotGitPath(rel string) bool {
	return rel == ".git" || strings.HasPrefix(rel, ".git/") || strings.Contains(rel, "/.git/")
}

func githubFiles(blobs []githubBlob, contents [][]byte) []File {
	files := make([]File, len(blobs))
	for i, blob := range blobs {
		files[i] = File{Path: blob.path, Content: bytes.Clone(contents[i]), Mode: blob.mode}
	}
	return files
}

type githubSourceSnapshot struct {
	ref          string
	rootTree     string
	entries      []githubTreeEntry
	byPath       map[string]githubTreeEntry
	owner        string
	repo         string
	locator      SourceLocator
	client       *githubClient
	cloned       []discoveredSkill
	discovered   *discoveredSource
	discoveryErr error
	storedBlobs  map[string][]byte
}

type githubRemoteSkill struct {
	gitTreeSHA  string
	files       []File
	blobs       []githubBlob
	description string
}

func (l *sqliteLibrary) readGitHubSnapshot(ctx context.Context, source Source) (githubSourceSnapshot, error) {
	var apiErr error
	if owner, repo, ok := githubOwnerRepo(source.Locator.Repository); ok {
		snap, err := l.fetchGitHubCommitSnapshot(ctx, owner, repo, source.Locator)
		if err == nil {
			snap.locator = source.Locator
			return snap, nil
		}
		if ctx.Err() != nil {
			return githubSourceSnapshot{}, err
		}
		if !githubContentsUnavailable(err) {
			return githubSourceSnapshot{}, err
		}
		apiErr = err
	}
	discovered, err := fetchGitHubClone(ctx, source.Locator, "")
	if err != nil {
		return githubSourceSnapshot{}, githubCloneError(ctx, apiErr, err)
	}
	return githubSourceSnapshot{ref: discovered.ref, cloned: discovered.skills, locator: source.Locator}, nil
}

func (l *sqliteLibrary) fetchGitHubCommitSnapshot(ctx context.Context, owner, repo string, locator SourceLocator) (githubSourceSnapshot, error) {
	client := l.newGitHubClient()
	commit, rootTree, err := client.commit(ctx, owner, repo, locator.Branch)
	if err != nil {
		return githubSourceSnapshot{}, err
	}
	return githubSourceSnapshot{ref: commit, rootTree: rootTree, owner: owner, repo: repo, client: client}, nil
}

func (s *githubSourceSnapshot) loadTree(ctx context.Context) error {
	if s.byPath != nil {
		return nil
	}
	tree, err := s.client.recursiveTree(ctx, s.owner, s.repo, s.rootTree)
	if err != nil {
		return err
	}
	if tree.Truncated {
		return errGitHubContentsFallback
	}
	byPath := make(map[string]githubTreeEntry, len(tree.Tree))
	for _, entry := range tree.Tree {
		byPath[entry.Path] = entry
	}
	s.entries, s.byPath = tree.Tree, byPath
	return nil
}

func gitBlobSHA(content []byte) string {
	sum := sha1.New()
	fmt.Fprintf(sum, "blob %d\x00", len(content))
	sum.Write(content)
	return hex.EncodeToString(sum.Sum(nil))
}

func (s *githubSourceSnapshot) rememberGitBlobs(files []File) {
	if s.client == nil {
		return
	}
	s.storedBlobs = make(map[string][]byte, len(files))
	for _, file := range files {
		sha := gitBlobSHA(file.Content)
		s.storedBlobs[sha] = file.Content
		memoPut(s.client.memo, &s.client.memo.blobs, memoKey(memoBlob, s.owner, s.repo, sha), file.Content)
	}
}

func (s *githubSourceSnapshot) confirms(confirmedCommit string) bool {
	return confirmedCommit != "" && s.ref == confirmedCommit
}

func (s *githubSourceSnapshot) locate(ctx context.Context, skill Skill) (githubRemoteSkill, error) {
	if s.cloned == nil && s.discovered == nil {
		result, err := discoveredSource{}, s.loadTree(ctx)
		if err == nil {
			result, err = discoverFromGitHubTree(ctx, s, ".")
		}
		s.discovered = &result
		if err != nil && ctx.Err() == nil && githubContentsUnavailable(err) {
			err = githubCloneError(ctx, err, s.fallbackClone(ctx))
		}
		s.discoveryErr = err
	}
	if s.discoveryErr != nil {
		return githubRemoteSkill{}, s.discoveryErr
	}
	candidates := s.cloned
	if s.cloned == nil {
		candidates = s.discovered.skills
	}
	index := -1
	if skill.GitTreeSHA != "" {
		index = slices.IndexFunc(candidates, func(c discoveredSkill) bool { return c.gitTreeSHA == skill.GitTreeSHA })
	}
	if index < 0 {
		index = slices.IndexFunc(candidates, func(c discoveredSkill) bool { return c.name == skill.Name })
	}
	if index < 0 {
		return githubRemoteSkill{}, &Error{Code: NotFound, Message: fmt.Sprintf("source skill %q not found", skill.Name), Details: ErrorDetails{Name: skill.Name}}
	}
	found := candidates[index]
	return githubRemoteSkill{gitTreeSHA: found.gitTreeSHA, files: found.files, blobs: found.blobs, description: found.candidate.Description}, nil
}

func (s *githubSourceSnapshot) files(ctx context.Context, skill Skill, remote githubRemoteSkill) ([]File, error) {
	if remote.files != nil {
		return remote.files, nil
	}
	blobs := slices.Clone(remote.blobs)
	for i, blob := range blobs {
		if content, ok := s.storedBlobs[blob.sha]; ok {
			blobs[i].content = content
		}
	}
	contents, err := s.client.transferBlobs(ctx, s.owner, s.repo, s.ref, githubRepoSize(s.entries), blobs)
	if err == nil {
		return githubFiles(remote.blobs, contents), nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	if !githubContentsUnavailable(err) {
		return nil, githubMaterializeError(err)
	}
	if cloneErr := s.fallbackClone(ctx); cloneErr != nil {
		return nil, githubCloneError(ctx, err, cloneErr)
	}
	relocated, locErr := s.locate(ctx, skill)
	if locErr != nil {
		return nil, locErr
	}
	return relocated.files, nil
}

func (s *githubSourceSnapshot) fallbackClone(ctx context.Context) error {
	if s.cloned != nil {
		return nil
	}
	locator := s.locator
	locator.Branch = s.ref
	discovered, err := fetchGitHubClone(ctx, locator, "")
	if err != nil {
		return err
	}
	s.cloned = discovered.skills
	s.ref = discovered.ref
	return nil
}

func githubDirTreeSHA(dir, rootTree string, byPath map[string]githubTreeEntry) (string, error) {
	if dir == "" || dir == "." {
		if !fullCommit.MatchString(rootTree) {
			return "", errGitHubContentsFallback
		}
		return strings.ToLower(rootTree), nil
	}
	entry, ok := byPath[dir]
	if !ok || entry.Type != "tree" {
		return "", errGitHubContentsFallback
	}
	sha := strings.ToLower(entry.SHA)
	if !fullCommit.MatchString(sha) {
		return "", errGitHubContentsFallback
	}
	return sha, nil
}

func githubSkillHasUpdate(ctx context.Context, snap *githubSourceSnapshot, skill Skill, confirmedCommit string) (bool, error) {
	if snap.confirms(confirmedCommit) {
		return false, nil
	}
	remote, err := snap.locate(ctx, skill)
	if err != nil {
		return false, err
	}
	if skill.GitTreeSHA != "" && remote.gitTreeSHA != "" {
		return skill.GitTreeSHA != remote.gitTreeSHA, nil
	}
	files, err := snap.files(ctx, skill, remote)
	if err != nil {
		return false, err
	}
	return !skillfile.Equal(skill.Files, files), nil
}

func (l *sqliteLibrary) materializeGitHubSkills(ctx context.Context, state *previewState, skills []discoveredSkill) error {
	owner, repo, _ := githubOwnerRepo(state.source.Locator.Repository)
	var all []githubBlob
	for _, skill := range skills {
		all = append(all, skill.blobs...)
	}
	if len(all) == 0 {
		return nil
	}
	contents, err := l.newGitHubClient().transferBlobs(ctx, owner, repo, state.ref, state.githubRepoSize, all)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		if !githubContentsUnavailable(err) {
			return githubMaterializeError(err)
		}
		return githubCloneError(ctx, err, materializeGitHubClone(ctx, state, skills))
	}
	for i, skill := range skills {
		if len(skill.blobs) == 0 {
			continue
		}
		skills[i].files = githubFiles(skill.blobs, contents)
		contents = contents[len(skill.blobs):]
		skills[i].blobs = nil
	}
	return nil
}

func materializeGitHubClone(ctx context.Context, state *previewState, skills []discoveredSkill) error {
	locator := state.source.Locator
	locator.Branch = state.ref
	discovered, err := fetchGitHubClone(ctx, locator, state.subpath)
	if err != nil {
		return err
	}
	for i, skill := range skills {
		index := slices.IndexFunc(discovered.skills, func(cloned discoveredSkill) bool {
			return cloned.candidate.Path == skill.candidate.Path && cloned.gitTreeSHA == skill.gitTreeSHA
		})
		if index < 0 {
			return githubMaterializeError(errGitHubContentsFallback)
		}
		skills[i].files = discovered.skills[index].files
		skills[i].blobs = nil
	}
	return nil
}

func githubCloneError(ctx context.Context, apiErr, cloneErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if IsCode(cloneErr, SourceUnreachable) && IsCode(githubMaterializeError(apiErr), Permission) {
		return githubMaterializeError(apiErr)
	}
	return cloneErr
}
