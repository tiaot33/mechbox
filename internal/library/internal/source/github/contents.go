package github

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
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

const githubAPIHost = "api.github.com"

var errGitHubContentsFallback = errors.New("github contents api unavailable")

type githubBlob struct {
	path     string
	repoPath string
	sha      string
	size     int64
	mode     skillfile.Mode
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
	token          atomic.Pointer[string]
}

type githubClient struct {
	useToken atomic.Bool
	useCLI   atomic.Bool
	auth     *githubAuth
	memo     *githubObjectMemo
}

func (c *Client) session() *githubClient {
	client := &githubClient{auth: &c.auth, memo: &c.memo}
	client.useToken.Store(c.auth.useToken.Load())
	client.useCLI.Store(c.auth.useCLI.Load())
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

func ownerRepo(repository string) (owner, repo string, ok bool) {
	rest, ok := strings.CutPrefix(repository, "https://github.com/")
	if !ok {
		return "", "", false
	}
	rest = strings.TrimSuffix(rest, ".git")
	owner, repo, ok = strings.Cut(rest, "/")
	return owner, repo, ok && owner != "" && repo != "" && !strings.Contains(repo, "/")
}

func (a *githubAuth) currentToken() string {
	if token := a.token.Load(); token != nil && *token != "" {
		return *token
	}
	return cmp.Or(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
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
	if token := c.auth.currentToken(); token != "" {
		c.useToken.Store(true)
		err = httpGet(token)
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
		return fmt.Errorf("fetch source: authentication failed: %w", source.ErrPermission)
	}
	return fmt.Errorf("fetch source: %w", source.ErrUnreachable)
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
			return "", &source.NotFoundError{What: source.MissingSubpath}
		}
		if entries[index].Type != "tree" {
			return "", invalidInput("source subpath must contain only real directories")
		}
	}
	return subpath, nil
}

type githubSourceView struct {
	snap *githubSnapshot
	dirs map[string][]discovery.Entry
}

func newGitHubSourceView(snap *githubSnapshot) githubSourceView {
	v := githubSourceView{snap: snap, dirs: map[string][]discovery.Entry{".": {}}}
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
		v.dirs[parent] = append(v.dirs[parent], discovery.Entry{Name: path.Base(entry.Path), Mode: mode})
	}
	for dir := range v.dirs {
		slices.SortFunc(v.dirs[dir], func(a, b discovery.Entry) int { return cmp.Compare(a.Name, b.Name) })
	}
	return v
}

func (v githubSourceView) ReadDir(dir string) ([]discovery.Entry, error) {
	entries, ok := v.dirs[dir]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return entries, nil
}

func (v githubSourceView) ReadFile(ctx context.Context, name string) ([]byte, error) {
	entry, ok := v.snap.byPath[name]
	if !ok || !githubRegularBlob(entry) {
		return nil, fs.ErrNotExist
	}
	contents, err := v.snap.client.transferBlobs(ctx, v.snap.owner, v.snap.repo, v.snap.ref, 0, []githubBlob{{repoPath: entry.Path, sha: entry.SHA, content: v.snap.storedBlobs[entry.SHA]}})
	if err != nil {
		return nil, githubDiscoveryError(err)
	}
	return contents[0], nil
}

func (v githubSourceView) ReadFiles(ctx context.Context, names []string) ([][]byte, []error) {
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
			errs[i] = githubDiscoveryError(err)
		}
	}
	return contents, errs
}

func githubDiscoveryError(err error) error {
	if githubContentsUnavailable(err) {
		return discovery.Abort(err)
	}
	return err
}

func discoverFromGitHubTree(ctx context.Context, snap *githubSnapshot, root string) (discoveredSource, error) {
	loaded := map[string]discoveredSkill{}
	candidates, diagnostics, err := discovery.Scan(ctx, newGitHubSourceView(snap), root, func(candidate discovery.Candidate, filename string, content []byte) error {
		blobs, treeSHA, err := githubSkillBlobs(snap.entries, candidate.Path, snap.rootTree, snap.byPath)
		if err != nil {
			return githubDiscoveryError(err)
		}
		for i, blob := range blobs {
			if blob.path == filename {
				blobs[i].content = content
				break
			}
		}
		loaded[candidate.Path] = discoveredSkill{blobs: blobs, gitTreeSHA: treeSHA}
		return nil
	})
	if err != nil {
		return discoveredSource{}, err
	}
	skills := make([]discoveredSkill, len(candidates))
	for i, candidate := range candidates {
		skill := loaded[candidate.Path]
		skill.name, skill.candidate = candidate.Name, candidate
		skills[i] = skill
	}
	return discoveredSource{skills: skills, diagnostics: diagnostics}, nil
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
		mode := skillfile.ModeRegular
		if entry.Mode == "100755" {
			mode = skillfile.ModeExecutable
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

func githubFiles(blobs []githubBlob, contents [][]byte) []skillfile.File {
	files := make([]skillfile.File, len(blobs))
	for i, blob := range blobs {
		files[i] = skillfile.File{Path: blob.path, Content: bytes.Clone(contents[i]), Mode: blob.mode}
	}
	return files
}

type githubSnapshot struct {
	ref          string
	rootTree     string
	entries      []githubTreeEntry
	byPath       map[string]githubTreeEntry
	owner        string
	repo         string
	locator      repoLocator
	subpath      string
	client       *githubClient
	skills       []discoveredSkill
	diagnostics  []discovery.Diagnostic
	discovered   bool
	discoveryErr error
	storedBlobs  map[string][]byte
}

type githubState struct {
	Commit string `json:"commit,omitempty"`
	Tree   string `json:"tree,omitempty"`
}

func decodeGitHubState(raw source.State) githubState {
	var state githubState
	if len(raw) > 0 && json.Unmarshal(raw, &state) != nil {
		return githubState{}
	}
	return state
}

func encodeGitHubState(commit, tree string) source.State {
	encoded, _ := json.Marshal(githubState{Commit: commit, Tree: tree})
	return encoded
}

func (c *Client) Open(ctx context.Context, loc source.Locator, subpath string, _ source.State) (source.Snapshot, error) {
	locator, err := decodeLocator(loc)
	if err != nil {
		return nil, err
	}
	s := &githubSnapshot{locator: locator, subpath: subpath}
	var apiErr error
	if owner, repo, ok := ownerRepo(locator.Repository); ok {
		session := c.session()
		commit, rootTree, err := session.commit(ctx, owner, repo, locator.Branch)
		if err == nil {
			s.ref, s.rootTree, s.owner, s.repo, s.client = commit, rootTree, owner, repo, session
			return s, nil
		}
		if ctx.Err() != nil || !githubContentsUnavailable(err) {
			return nil, err
		}
		apiErr = err
	}
	if err := s.clone(ctx, locator.Branch); err != nil {
		return nil, githubCloneError(ctx, apiErr, err)
	}
	return s, nil
}

func (s *githubSnapshot) Revision() string {
	return s.ref
}

func (s *githubSnapshot) Candidates(ctx context.Context) ([]discovery.Candidate, []discovery.Diagnostic, error) {
	if err := s.discover(ctx); err != nil {
		return nil, nil, err
	}
	if len(s.skills) == 0 {
		return nil, nil, discovery.NoCandidates(s.diagnostics)
	}
	candidates := make([]discovery.Candidate, len(s.skills))
	for i, skill := range s.skills {
		candidates[i] = skill.candidate
	}
	return candidates, s.diagnostics, nil
}

func (s *githubSnapshot) Fetch(ctx context.Context, names []string) ([]source.Skill, error) {
	if err := s.discover(ctx); err != nil {
		return nil, err
	}
	selected, err := selectSkills(s.skills, names)
	if err != nil {
		return nil, err
	}
	selected = slices.Clone(selected)
	if err := s.materialize(ctx, selected); err != nil {
		return nil, err
	}
	result := make([]source.Skill, len(selected))
	for i, skill := range selected {
		result[i] = source.Skill{Name: skill.name, Description: skill.candidate.Description, Files: skill.files, State: encodeGitHubState(s.ref, skill.gitTreeSHA)}
	}
	return result, nil
}

func (s *githubSnapshot) Check(ctx context.Context, have source.Have) (source.Status, source.State, error) {
	state := decodeGitHubState(have.State)
	if state.Commit != "" && state.Commit == s.ref {
		return source.Unchanged, have.State, nil
	}
	if err := s.discover(ctx); err != nil {
		return source.Unknown, nil, err
	}
	remote, err := selectSkills(s.skills, []string{have.Name})
	if err != nil {
		return source.Unknown, nil, err
	}
	tree := remote[0].gitTreeSHA
	switch {
	case state.Tree == "" || tree == "":
		s.rememberGitBlobs(have.Files)
		return source.Unknown, nil, nil
	case state.Tree == tree:
		return source.Unchanged, encodeGitHubState(s.ref, tree), nil
	default:
		s.rememberGitBlobs(have.Files)
		return source.Changed, nil, nil
	}
}

func (s *githubSnapshot) discover(ctx context.Context) error {
	if s.discovered {
		return s.discoveryErr
	}
	s.discovered = true
	s.discoveryErr = s.discoverTree(ctx)
	if s.discoveryErr != nil && ctx.Err() == nil && githubContentsUnavailable(s.discoveryErr) {
		s.discoveryErr = githubCloneError(ctx, s.discoveryErr, s.clone(ctx, s.ref))
	}
	return s.discoveryErr
}

func (s *githubSnapshot) discoverTree(ctx context.Context) error {
	if err := s.loadTree(ctx); err != nil {
		return err
	}
	root, err := resolveGitHubSubpath(s.entries, s.subpath)
	if err != nil {
		return err
	}
	result, err := discoverFromGitHubTree(ctx, s, root)
	if err != nil {
		return err
	}
	s.skills, s.diagnostics = result.skills, result.diagnostics
	return nil
}

func (s *githubSnapshot) clone(ctx context.Context, ref string) error {
	locator := s.locator
	locator.Branch = ref
	discovered, err := fetchGitHubClone(ctx, locator, s.subpath)
	if err != nil {
		return err
	}
	s.ref, s.skills, s.diagnostics, s.discovered, s.discoveryErr = discovered.ref, discovered.skills, discovered.diagnostics, true, nil
	return nil
}

func (s *githubSnapshot) materialize(ctx context.Context, skills []discoveredSkill) error {
	var all []githubBlob
	for _, skill := range skills {
		if skill.files != nil {
			continue
		}
		for _, blob := range skill.blobs {
			if content, ok := s.storedBlobs[blob.sha]; ok && blob.content == nil {
				blob.content = content
			}
			all = append(all, blob)
		}
	}
	if len(all) == 0 {
		return nil
	}
	contents, err := s.client.transferBlobs(ctx, s.owner, s.repo, s.ref, githubRepoSize(s.entries), all)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		if !githubContentsUnavailable(err) {
			return githubMaterializeError(err)
		}
		if cloneErr := s.clone(ctx, s.ref); cloneErr != nil {
			return githubCloneError(ctx, err, cloneErr)
		}
		for i, skill := range skills {
			cloned, err := selectSkills(s.skills, []string{skill.name})
			if err != nil {
				return err
			}
			skills[i] = cloned[0]
		}
		return nil
	}
	for i, skill := range skills {
		if skill.files != nil {
			continue
		}
		skills[i].files = githubFiles(skill.blobs, contents)
		contents = contents[len(skill.blobs):]
		skills[i].blobs = nil
	}
	return nil
}

func (s *githubSnapshot) loadTree(ctx context.Context) error {
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

func (s *githubSnapshot) rememberGitBlobs(files []skillfile.File) {
	if s.client == nil {
		return
	}
	if s.storedBlobs == nil {
		s.storedBlobs = make(map[string][]byte, len(files))
	}
	for _, file := range files {
		sha := gitBlobSHA(file.Content)
		s.storedBlobs[sha] = file.Content
		memoPut(s.client.memo, &s.client.memo.blobs, memoKey(memoBlob, s.owner, s.repo, sha), file.Content)
	}
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

func githubCloneError(ctx context.Context, apiErr, cloneErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(cloneErr, source.ErrUnreachable) && errors.Is(githubMaterializeError(apiErr), source.ErrPermission) {
		return githubMaterializeError(apiErr)
	}
	return cloneErr
}

type discoveredSkill struct {
	name       string
	candidate  discovery.Candidate
	files      []skillfile.File
	gitTreeSHA string
	blobs      []githubBlob
}

func selectSkills(skills []discoveredSkill, selected []string) ([]discoveredSkill, error) {
	if len(selected) == 0 {
		return skills, nil
	}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		wanted[name] = true
	}
	result := make([]discoveredSkill, 0, len(selected))
	for _, skill := range skills {
		if wanted[skill.name] {
			result = append(result, skill)
			delete(wanted, skill.name)
		}
	}
	for _, name := range selected {
		if wanted[name] {
			return nil, &source.NotFoundError{What: source.MissingSkill, Name: name}
		}
	}
	return result, nil
}
