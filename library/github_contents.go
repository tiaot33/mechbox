package library

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
)

const githubAPIHost = "api.github.com"

var errGitHubContentsFallback = errors.New("github contents api unavailable")

var discoveryExcludedDirs = []string{".git", "node_modules", "dist", "build", "__pycache__"}

type githubBlob struct {
	path    string // Skill 内相对路径
	sha     string
	mode    FileMode
	content []byte // 已取到的正文;预览阶段通常只有 SKILL.md
}

type githubHTTPError struct {
	status      int
	rateLimited bool
}

func (e *githubHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d", e.status)
}

type githubClient struct {
	token    string
	useToken bool
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
	SHA       string            `json:"sha"`
	Truncated bool              `json:"truncated"`
	Tree      []githubTreeEntry `json:"tree"`
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
}

type githubBlobResponse struct {
	SHA       string `json:"sha"`
	Encoding  string `json:"encoding"`
	Content   string `json:"content"`
	Size      int    `json:"size"`
	Truncated bool   `json:"truncated"`
}

// githubOwnerRepo 仅识别 github.com 上的仓库;其他远端走 git 回退。
func githubOwnerRepo(repository string) (owner, repo string, ok bool) {
	parsed, err := parseGitHubInput(repository, ResolveSourceOptions{})
	if err != nil {
		return "", "", false
	}
	rest, ok := strings.CutPrefix(parsed.repository, "https://github.com/")
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

// fetchGitHubContents 用 GitHub Git Database API 按已解析的 owner/repo 与 ref 取 recursive tree。
func fetchGitHubContents(ctx context.Context, owner, repo string, locator SourceLocator, subpath string, preview bool) (discoveredSource, error) {
	client := &githubClient{token: githubToken()}
	commit, rootTree, err := client.commit(ctx, owner, repo, locator.Branch)
	if err != nil {
		return discoveredSource{}, err
	}
	tree, err := client.recursiveTree(ctx, owner, repo, rootTree)
	if err != nil {
		return discoveredSource{}, err
	}
	if tree.Truncated {
		return discoveredSource{}, errGitHubContentsFallback
	}
	root, err := resolveGitHubSubpath(tree.Tree, subpath)
	if err != nil {
		return discoveredSource{}, err
	}
	result, err := discoverFromGitHubTree(ctx, client, owner, repo, tree.Tree, root, rootTree, preview)
	if err != nil {
		return discoveredSource{}, err
	}
	result.ref = commit
	result.githubOwner = owner
	result.githubRepo = repo
	return result, nil
}

func (c *githubClient) commit(ctx context.Context, owner, repo, ref string) (commit, tree string, err error) {
	var payload githubCommitResponse
	if err := c.get(ctx, githubAPIURL("/repos/%s/%s/commits/%s", owner, repo, ref), &payload); err != nil {
		return "", "", err
	}
	commit = strings.ToLower(strings.TrimSpace(payload.SHA))
	tree = strings.ToLower(strings.TrimSpace(payload.Commit.Tree.SHA))
	if !fullCommit.MatchString(commit) || !fullCommit.MatchString(tree) {
		return "", "", errGitHubContentsFallback
	}
	return commit, tree, nil
}

func (c *githubClient) recursiveTree(ctx context.Context, owner, repo, sha string) (githubTreeResponse, error) {
	var payload githubTreeResponse
	location := githubAPIURL("/repos/%s/%s/git/trees/%s", owner, repo, sha) + "?recursive=1"
	if err := c.get(ctx, location, &payload); err != nil {
		return githubTreeResponse{}, err
	}
	return payload, nil
}

func (c *githubClient) blob(ctx context.Context, owner, repo, sha string) ([]byte, error) {
	var payload githubBlobResponse
	if err := c.get(ctx, githubAPIURL("/repos/%s/%s/git/blobs/%s", owner, repo, sha), &payload); err != nil {
		return nil, err
	}
	if payload.Truncated || !strings.EqualFold(payload.Encoding, "base64") {
		return nil, errGitHubContentsFallback
	}
	content, err := decodeGitHubBlob(payload.Content)
	if err != nil || len(content) != payload.Size {
		return nil, errGitHubContentsFallback
	}
	return content, nil
}

func githubAPIURL(pattern string, owner, repo, object string) string {
	return "https://" + githubAPIHost + fmt.Sprintf(pattern, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(object))
}

func decodeGitHubBlob(content string) ([]byte, error) {
	trimmed := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, content)
	return base64.StdEncoding.DecodeString(trimmed)
}

func (c *githubClient) get(ctx context.Context, location string, dest any) error {
	if !c.useToken {
		err := githubAPIGetOnce(ctx, location, "", dest)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if c.token != "" && isGitHubAuthRetry(err) {
			c.useToken = true
		} else {
			return err
		}
	}
	if err := githubAPIGetOnce(ctx, location, c.token, dest); err != nil {
		return err
	}
	return nil
}

// githubContentsUnavailable 表示快路径无法给出完整结果,Fetch 应回退浅克隆。
func githubContentsUnavailable(err error) bool {
	if errors.Is(err, errGitHubContentsFallback) {
		return true
	}
	_, ok := errors.AsType[*githubHTTPError](err)
	return ok
}

// githubInstallError 把安装阶段的 blob 失败映射为稳定的门面错误码,不把 HTTP 正文或凭证写入 Message。
func githubInstallError(err error) error {
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
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return errGitHubContentsFallback
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "skills-tui")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errGitHubContentsFallback
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, maxFetchSize+1))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errGitHubContentsFallback
	}
	if len(body) > maxFetchSize {
		return errGitHubContentsFallback
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &githubHTTPError{status: response.StatusCode, rateLimited: githubRateLimited(response)}
	}
	if err := json.Unmarshal(body, dest); err != nil {
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

func discoverFromGitHubTree(ctx context.Context, client *githubClient, owner, repo string, entries []githubTreeEntry, root, rootTree string, preview bool) (discoveredSource, error) {
	byPath := make(map[string]githubTreeEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	var result discoveredSource
	skipped := []string{}
	isSkipped := func(dir string) bool {
		for _, prefix := range skipped {
			if prefix == "." || dir == prefix || strings.HasPrefix(dir, prefix+"/") {
				return true
			}
		}
		return false
	}
	for _, dir := range githubDiscoveryDirs(entries, root) {
		if err := ctx.Err(); err != nil {
			return discoveredSource{}, err
		}
		if isSkipped(dir) {
			continue
		}
		if dir != root && slices.Contains(discoveryExcludedDirs, path.Base(dir)) {
			skipped = append(skipped, dir)
			continue
		}
		skillMD := "SKILL.md"
		if dir != "." {
			skillMD = path.Join(dir, "SKILL.md")
		}
		entry, ok := byPath[skillMD]
		if !ok {
			continue
		}
		skipped = append(skipped, dir)
		if !githubRegularBlob(entry) {
			result.diagnostics = append(result.diagnostics, CandidateDiagnostic{Path: dir, Reason: "SKILL.md must be a regular file"})
			continue
		}
		content, err := client.blob(ctx, owner, repo, entry.SHA)
		if err != nil {
			return discoveredSource{}, err
		}
		previewSkill, err := candidateMetadata(content, dir)
		if err != nil {
			result.diagnostics = append(result.diagnostics, CandidateDiagnostic{Path: dir, Reason: err.Error()})
			continue
		}
		blobs, treeSHA, err := githubSkillBlobs(entries, dir, rootTree, byPath)
		if err != nil {
			return discoveredSource{}, err
		}
		for i, blob := range blobs {
			if blob.path == "SKILL.md" {
				blobs[i].content = content
				break
			}
		}
		skill := fetchedSkill{name: previewSkill.Name, preview: previewSkill, gitTreeSHA: treeSHA, blobs: blobs}
		if !preview {
			files, err := fetchGitHubSkillFiles(ctx, client, owner, repo, blobs)
			if err != nil {
				return discoveredSource{}, err
			}
			skill.files = files
			skill.blobs = nil
		}
		result.skills = append(result.skills, skill)
	}
	return finishDiscovery(result)
}

func githubDiscoveryDirs(entries []githubTreeEntry, root string) []string {
	seen := map[string]bool{root: true}
	dirs := []string{root}
	add := func(dir string) {
		for {
			if !githubUnderRoot(dir, root) {
				return
			}
			if seen[dir] {
				if dir == root {
					return
				}
				parent := path.Dir(dir)
				if parent == dir {
					return
				}
				dir = parent
				continue
			}
			seen[dir] = true
			dirs = append(dirs, dir)
			if dir == root {
				return
			}
			parent := path.Dir(dir)
			if parent == dir {
				return
			}
			dir = parent
		}
	}
	for _, entry := range entries {
		if !githubUnderRoot(entry.Path, root) {
			continue
		}
		if entry.Type == "tree" {
			add(entry.Path)
			continue
		}
		add(path.Dir(entry.Path))
	}
	slices.Sort(dirs)
	return dirs
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
	treeSHA := rootTree
	if skillPath != "" && skillPath != "." {
		entry, ok := byPath[skillPath]
		if !ok || entry.Type != "tree" || !fullCommit.MatchString(strings.ToLower(entry.SHA)) {
			return nil, "", errGitHubContentsFallback
		}
		treeSHA = strings.ToLower(entry.SHA)
	} else if !fullCommit.MatchString(treeSHA) {
		return nil, "", errGitHubContentsFallback
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
		blobs = append(blobs, githubBlob{path: rel, sha: entry.SHA, mode: mode})
	}
	slices.SortFunc(blobs, func(a, b githubBlob) int { return strings.Compare(a.path, b.path) })
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

func fetchGitHubSkillFiles(ctx context.Context, client *githubClient, owner, repo string, blobs []githubBlob) ([]File, error) {
	files := make([]File, 0, len(blobs))
	for _, blob := range blobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		content := blob.content
		if content == nil {
			var err error
			content, err = client.blob(ctx, owner, repo, blob.sha)
			if err != nil {
				return nil, err
			}
		}
		files = append(files, File{Path: blob.path, Content: bytes.Clone(content), Mode: blob.mode})
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// githubSourceSnapshot 是一次 GitHub 来源读取:API 只拿到 tree,或克隆回退后已有各 Skill 文件。
type githubSourceSnapshot struct {
	ref      string
	rootTree string
	entries  []githubTreeEntry
	byPath   map[string]githubTreeEntry
	owner    string
	repo     string
	locator  SourceLocator
	client   *githubClient
	cloned   []fetchedSkill
}

type githubRemoteSkill struct {
	path       string
	gitTreeSHA string
	files      []File
	hasFiles   bool
}

// readGitHubSnapshot 对同一 Source 只远程读一次 tree(或一次克隆),供检查与更新共用。
func readGitHubSnapshot(ctx context.Context, source Source) (githubSourceSnapshot, error) {
	if owner, repo, ok := githubOwnerRepo(source.Locator.Repository); ok {
		snap, err := fetchGitHubTreeSnapshot(ctx, owner, repo, source.Locator)
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
	}
	discovered, err := fetchGitHubClone(ctx, source.Locator, "")
	if err != nil {
		return githubSourceSnapshot{}, err
	}
	return githubSourceSnapshot{ref: discovered.ref, cloned: discovered.skills, locator: source.Locator}, nil
}

func fetchGitHubTreeSnapshot(ctx context.Context, owner, repo string, locator SourceLocator) (githubSourceSnapshot, error) {
	client := &githubClient{token: githubToken()}
	commit, rootTree, err := client.commit(ctx, owner, repo, locator.Branch)
	if err != nil {
		return githubSourceSnapshot{}, err
	}
	tree, err := client.recursiveTree(ctx, owner, repo, rootTree)
	if err != nil {
		return githubSourceSnapshot{}, err
	}
	if tree.Truncated {
		return githubSourceSnapshot{}, errGitHubContentsFallback
	}
	byPath := make(map[string]githubTreeEntry, len(tree.Tree))
	for _, entry := range tree.Tree {
		byPath[entry.Path] = entry
	}
	return githubSourceSnapshot{
		ref:      commit,
		rootTree: rootTree,
		entries:  tree.Tree,
		byPath:   byPath,
		owner:    owner,
		repo:     repo,
		client:   client,
	}, nil
}

func (s *githubSourceSnapshot) locate(ctx context.Context, skill Skill) (githubRemoteSkill, error) {
	if s.cloned != nil {
		selected, err := selectSkills(s.cloned, []string{skill.Name})
		if err != nil {
			return githubRemoteSkill{}, err
		}
		found := selected[0]
		return githubRemoteSkill{path: found.preview.Path, gitTreeSHA: found.gitTreeSHA, files: found.files, hasFiles: true}, nil
	}
	dirs := githubCandidateDirs(s.entries, ".", s.byPath)
	if skill.GitTreeSHA != "" {
		for _, dir := range dirs {
			sha, err := githubDirTreeSHA(dir, s.rootTree, s.byPath)
			if err != nil {
				continue
			}
			if sha == skill.GitTreeSHA {
				return githubRemoteSkill{path: dir, gitTreeSHA: sha}, nil
			}
		}
	}
	return s.locateByName(ctx, skill, dirs)
}

func (s *githubSourceSnapshot) locateByName(ctx context.Context, skill Skill, dirs []string) (githubRemoteSkill, error) {
	var found *githubRemoteSkill
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return githubRemoteSkill{}, err
		}
		skillMD := "SKILL.md"
		if dir != "." {
			skillMD = path.Join(dir, "SKILL.md")
		}
		entry, ok := s.byPath[skillMD]
		if !ok || !githubRegularBlob(entry) {
			continue
		}
		content, err := s.client.blob(ctx, s.owner, s.repo, entry.SHA)
		if err != nil {
			if ctx.Err() != nil {
				return githubRemoteSkill{}, err
			}
			if !githubContentsUnavailable(err) {
				return githubRemoteSkill{}, githubInstallError(err)
			}
			if cloneErr := s.fallbackClone(ctx); cloneErr != nil {
				return githubRemoteSkill{}, cloneErr
			}
			return s.locate(ctx, skill)
		}
		preview, err := candidateMetadata(content, dir)
		if err != nil || preview.Name != skill.Name {
			continue
		}
		sha, err := githubDirTreeSHA(dir, s.rootTree, s.byPath)
		if err != nil {
			return githubRemoteSkill{}, githubInstallError(err)
		}
		if found != nil {
			return githubRemoteSkill{}, &Error{Code: InvalidArgument, Message: fmt.Sprintf("ambiguous skill name %q", skill.Name)}
		}
		found = &githubRemoteSkill{path: dir, gitTreeSHA: sha}
	}
	if found == nil {
		return githubRemoteSkill{}, &Error{Code: NotFound, Message: fmt.Sprintf("source skill %q not found", skill.Name)}
	}
	return *found, nil
}

func (s *githubSourceSnapshot) files(ctx context.Context, skill Skill, remote githubRemoteSkill) ([]File, error) {
	if remote.hasFiles {
		return remote.files, nil
	}
	blobs, _, err := githubSkillBlobs(s.entries, remote.path, s.rootTree, s.byPath)
	if err == nil {
		var files []File
		files, err = fetchGitHubSkillFiles(ctx, s.client, s.owner, s.repo, blobs)
		if err == nil {
			return files, nil
		}
	}
	if ctx.Err() != nil {
		return nil, err
	}
	if !githubContentsUnavailable(err) {
		return nil, githubInstallError(err)
	}
	if cloneErr := s.fallbackClone(ctx); cloneErr != nil {
		return nil, cloneErr
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
	discovered, err := fetchGitHubClone(ctx, s.locator, "")
	if err != nil {
		return err
	}
	s.cloned = discovered.skills
	s.ref = discovered.ref
	return nil
}

func githubCandidateDirs(entries []githubTreeEntry, root string, byPath map[string]githubTreeEntry) []string {
	var dirs []string
	skipped := []string{}
	isSkipped := func(dir string) bool {
		for _, prefix := range skipped {
			if prefix == "." || dir == prefix || strings.HasPrefix(dir, prefix+"/") {
				return true
			}
		}
		return false
	}
	for _, dir := range githubDiscoveryDirs(entries, root) {
		if isSkipped(dir) {
			continue
		}
		if dir != root && slices.Contains(discoveryExcludedDirs, path.Base(dir)) {
			skipped = append(skipped, dir)
			continue
		}
		skillMD := "SKILL.md"
		if dir != "." {
			skillMD = path.Join(dir, "SKILL.md")
		}
		entry, ok := byPath[skillMD]
		if !ok {
			continue
		}
		skipped = append(skipped, dir)
		if !githubRegularBlob(entry) {
			continue
		}
		dirs = append(dirs, dir)
	}
	return dirs
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

func githubSkillHasUpdate(ctx context.Context, snap *githubSourceSnapshot, skill Skill) (bool, error) {
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
	return !filesEqual(skill.Files, files), nil
}

// materializeGitHubSkills 在钉住的 commit 上取所选 Skill 的 blob;失败不入库,由调用方保留句柄。
func materializeGitHubSkills(ctx context.Context, owner, repo string, skills []fetchedSkill) error {
	client := &githubClient{token: githubToken()}
	for i, skill := range skills {
		if len(skill.blobs) == 0 {
			continue
		}
		files, err := fetchGitHubSkillFiles(ctx, client, owner, repo, skill.blobs)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			return githubInstallError(err)
		}
		skills[i].files = files
		skills[i].blobs = nil
	}
	return nil
}
