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
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const githubAPIHost = "api.github.com"

var errGitHubContentsFallback = errors.New("github contents api unavailable")

type githubBlob struct {
	path    string // Skill 内相对路径
	sha     string
	mode    FileMode
	content []byte // 已取到的正文;预览阶段通常只有 SKILL.md
}

type githubHTTPError struct {
	status      int
	rateLimited bool
	reset       int64
}

func (e *githubHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d", e.status)
}

// githubAuth 是 Library 实例级的 GitHub 鉴权阶梯状态:匿名 → 环境 token → gh CLI。
// 一个 Library 实例只爬一次阶梯,后续操作直接从已确认可用的那一级发请求,实例内不会降级;
// Close 清空,重新 Open 从匿名重新开始。
// 只有被明确拒绝(见 githubAuthDenied)才会让下一级在实例上生效;限流只写避让窗口,窗口到期后回到匿名。
type githubAuth struct {
	anonymousReset atomic.Int64 // 匿名 API 限流窗口的 Unix 秒,到期前跳过匿名请求
	useToken       atomic.Bool  // 匿名被明确拒绝:后续不再试匿名
	useCLI         atomic.Bool  // HTTP 路径被拒且 gh CLI 已成功:后续直接走 CLI
}

// githubClient 是一次操作内的 GitHub API 读取器:阶梯位置从实例级 githubAuth 起步,
// 操作内因限流或含义模糊的 403 爬到的更高一级只在本客户端内记住。
type githubClient struct {
	token    string
	useToken bool
	useCLI   bool
	auth     *githubAuth
}

// newGitHubClient 是构造 GitHub 客户端的唯一入口;Fetch / InstallFetch 与检查 / 更新路径共用。
func (l *sqliteLibrary) newGitHubClient() *githubClient {
	return &githubClient{
		token:    githubToken(),
		useToken: l.githubAuth.useToken.Load(),
		useCLI:   l.githubAuth.useCLI.Load(),
		auth:     &l.githubAuth,
	}
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
}

type githubBlobResponse struct {
	Encoding  string `json:"encoding"`
	Content   string `json:"content"`
	Size      int    `json:"size"`
	Truncated bool   `json:"truncated"`
}

// githubOwnerRepo 仅识别 github.com 上的仓库;其他远端走 git 回退。
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

// fetchGitHubContents 用 GitHub Git Database API 按已解析的 owner/repo 与 ref 取 recursive tree。
// 只取各候选 SKILL.md;安装再按 blob SHA 取所选目录。
func (l *sqliteLibrary) fetchGitHubContents(ctx context.Context, owner, repo string, locator SourceLocator, subpath string) (discoveredSource, error) {
	snap, err := l.fetchGitHubTreeSnapshot(ctx, owner, repo, locator)
	if err != nil {
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
	return finishDiscovery(result)
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
	content, err := base64.StdEncoding.DecodeString(payload.Content)
	if err != nil || len(content) != payload.Size {
		return nil, errGitHubContentsFallback
	}
	return content, nil
}

func githubAPIURL(pattern string, owner, repo, object string) string {
	return "https://" + githubAPIHost + fmt.Sprintf(pattern, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(object))
}

func (c *githubClient) get(ctx context.Context, location string, dest any) error {
	if c.useCLI {
		return githubCLIGet(ctx, location, dest)
	}
	err := errGitHubContentsFallback
	if !c.useToken && time.Now().Unix() >= c.auth.anonymousReset.Load() {
		err = githubAPIGetOnce(ctx, location, "", dest)
		if httpErr, ok := errors.AsType[*githubHTTPError](err); ok && httpErr.rateLimited {
			c.auth.anonymousReset.Store(httpErr.reset)
		}
		if err == nil || ctx.Err() != nil || !isGitHubAuthRetry(err) {
			return err
		}
		if githubAuthDenied(err) {
			c.auth.useToken.Store(true)
		}
	}
	if c.token != "" {
		c.useToken = true
		err = githubAPIGetOnce(ctx, location, c.token, dest)
		if err == nil || ctx.Err() != nil || !isGitHubAuthRetry(err) {
			return err
		}
	}
	if cliErr := githubCLIGet(ctx, location, dest); cliErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	c.useCLI = true
	if githubAuthDenied(err) {
		c.auth.useCLI.Store(true)
	}
	return nil
}

// githubAuthDenied 表示这一级被明确拒绝:私有仓库对匿名返回 404,失效 token 返回 401。
// 限流与含义模糊的 403(次级限流、SSO 等)不算,它们不值得让整个实例记住下一级。
func githubAuthDenied(err error) bool {
	httpErr, ok := errors.AsType[*githubHTTPError](err)
	return ok && !httpErr.rateLimited && (httpErr.status == http.StatusUnauthorized || httpErr.status == http.StatusNotFound)
}

// githubCLIGet 使用 gh 自己的登录态,不导出凭证,也不向门面透传任何命令输出。
func githubCLIGet(ctx context.Context, location string, dest any) error {
	commandCtx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	endpoint := strings.TrimPrefix(location, "https://"+githubAPIHost)
	command := exec.CommandContext(commandCtx, "gh", "api", endpoint, "--method", "GET", "--hostname", "github.com")
	// 环境 token 已在 HTTP 路径试过,不能让失效值遮住 gh 保存的登录态。
	command.Env = append(os.Environ(), "GITHUB_TOKEN=", "GH_TOKEN=", "GH_PROMPT_DISABLED=1")
	command.WaitDelay = time.Second
	cancelGitProcess(command)
	body, err := command.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || len(body) > maxFetchSize || json.Unmarshal(body, dest) != nil {
		return errGitHubContentsFallback
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
		// 限流响应恒带 Reset;解析失败存 0 即不避让。
		reset, _ := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64)
		return &githubHTTPError{status: response.StatusCode, rateLimited: githubRateLimited(response), reset: reset}
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

// githubSourceView 从平铺 tree 建目录索引,不为发现下载辅助文件。
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
	return v.snap.client.blob(ctx, v.snap.owner, v.snap.repo, entry.SHA)
}

func discoverFromGitHubTree(ctx context.Context, snap *githubSourceSnapshot, root string) (discoveredSource, error) {
	return discoverCandidates(ctx, newGitHubSourceView(snap), root, func(skill *fetchedSkill, filename string, content []byte) error {
		blobs, treeSHA, err := githubSkillBlobs(snap.entries, skill.preview.Path, snap.rootTree, snap.byPath)
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
		blobs = append(blobs, githubBlob{path: rel, sha: entry.SHA, mode: mode})
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
	return files, nil
}

// githubSourceSnapshot 是一次 GitHub 来源读取:API 只拿到 tree,或克隆回退后已有各 Skill 文件。
type githubSourceSnapshot struct {
	ref          string
	rootTree     string
	entries      []githubTreeEntry
	byPath       map[string]githubTreeEntry
	owner        string
	repo         string
	locator      SourceLocator
	client       *githubClient
	cloned       []fetchedSkill
	discovered   *discoveredSource
	discoveryErr error
}

type githubRemoteSkill struct {
	gitTreeSHA string
	files      []File
	blobs      []githubBlob
}

// readGitHubSnapshot 对同一 Source 只远程读一次 tree(或一次克隆),供检查与更新共用。
func (l *sqliteLibrary) readGitHubSnapshot(ctx context.Context, source Source) (githubSourceSnapshot, error) {
	var apiErr error
	if owner, repo, ok := githubOwnerRepo(source.Locator.Repository); ok {
		snap, err := l.fetchGitHubTreeSnapshot(ctx, owner, repo, source.Locator)
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

func (l *sqliteLibrary) fetchGitHubTreeSnapshot(ctx context.Context, owner, repo string, locator SourceLocator) (githubSourceSnapshot, error) {
	client := l.newGitHubClient()
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
	if s.cloned == nil && s.discovered == nil {
		result, err := discoverFromGitHubTree(ctx, s, ".")
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
		index = slices.IndexFunc(candidates, func(c fetchedSkill) bool { return c.gitTreeSHA == skill.GitTreeSHA })
	}
	if index < 0 {
		index = slices.IndexFunc(candidates, func(c fetchedSkill) bool { return c.name == skill.Name })
	}
	if index < 0 {
		return githubRemoteSkill{}, &Error{Code: NotFound, Message: fmt.Sprintf("source skill %q not found", skill.Name)}
	}
	found := candidates[index]
	return githubRemoteSkill{gitTreeSHA: found.gitTreeSHA, files: found.files, blobs: found.blobs}, nil
}

func (s *githubSourceSnapshot) files(ctx context.Context, skill Skill, remote githubRemoteSkill) ([]File, error) {
	if remote.files != nil {
		return remote.files, nil
	}
	files, err := fetchGitHubSkillFiles(ctx, s.client, s.owner, s.repo, remote.blobs)
	if err == nil {
		return files, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	if !githubContentsUnavailable(err) {
		return nil, githubInstallError(err)
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
func (l *sqliteLibrary) materializeGitHubSkills(ctx context.Context, session *fetchSession, skills []fetchedSkill) error {
	owner, repo, _ := githubOwnerRepo(session.source.Locator.Repository)
	client := l.newGitHubClient()
	for i, skill := range skills {
		if len(skill.blobs) == 0 {
			continue
		}
		files, err := fetchGitHubSkillFiles(ctx, client, owner, repo, skill.blobs)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			if !githubContentsUnavailable(err) {
				return githubInstallError(err)
			}
			return githubCloneError(ctx, err, materializeGitHubClone(ctx, session, skills))
		}
		skills[i].files = files
		skills[i].blobs = nil
	}
	return nil
}

// materializeGitHubClone 在钉住的 commit 上按预览时的 subpath 重新克隆发现,再按路径与 tree SHA 匹配所选 Skill。
func materializeGitHubClone(ctx context.Context, session *fetchSession, skills []fetchedSkill) error {
	locator := session.source.Locator
	locator.Branch = session.ref
	discovered, err := fetchGitHubClone(ctx, locator, session.subpath)
	if err != nil {
		return err
	}
	for i, skill := range skills {
		index := slices.IndexFunc(discovered.skills, func(cloned fetchedSkill) bool {
			return cloned.preview.Path == skill.preview.Path && cloned.gitTreeSHA == skill.gitTreeSHA
		})
		if index < 0 {
			return githubInstallError(errGitHubContentsFallback)
		}
		skills[i].files = discovered.skills[index].files
		skills[i].blobs = nil
	}
	return nil
}

// githubCloneError 保留快路径明确的认证失败,不让克隆不可达把它变成网络错误。
func githubCloneError(ctx context.Context, apiErr, cloneErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if IsCode(cloneErr, SourceUnreachable) && IsCode(githubInstallError(apiErr), Permission) {
		return githubInstallError(apiErr)
	}
	return cloneErr
}
