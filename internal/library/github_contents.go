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
)

const githubAPIHost = "api.github.com"

var errGitHubContentsFallback = errors.New("github contents api unavailable")

type githubBlob struct {
	path     string // Skill 内相对路径
	repoPath string // 仓库内相对路径,用于按 commit 读取 raw
	sha      string
	size     int64 // tree 给出的字节数,缺失为 0
	mode     FileMode
	content  []byte // 已取到的正文;预览阶段通常只有 SKILL.md
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
// 一个 Library 实例只爬一次阶梯:某一级被明确拒绝(见 githubAuthDenied)且更高一级成功后,
// 实例记住"从那一级起步",后续操作不再重试被拒的那级;Close 清空,重新 Open 从匿名重新开始。
// 限流只写避让窗口,窗口到期后回到匿名;token 限流时经 CLI 成功不粘住 CLI(限流是暂时的),
// 所以 useToken 为 true 时 useCLI 仍可能为 false,这一级的位置由两个标志共同决定。
type githubAuth struct {
	anonymousReset atomic.Int64 // 匿名 API 限流窗口的 Unix 秒,到期前跳过匿名请求
	useToken       atomic.Bool  // 匿名被明确拒绝且更高一级已成功:后续不再试匿名
	useCLI         atomic.Bool  // HTTP 路径被明确拒绝且 gh CLI 已成功:后续直接走 CLI
}

// githubClient 是一次操作内的 GitHub API 读取器:阶梯位置从实例级 githubAuth 起步,
// 操作内因限流或含义模糊的 403 爬到的更高一级只在本客户端内记住。
// 同一操作内的并发读取共用一个客户端,阶梯位置因此用原子值。
// 不可变 git 对象的进程内 memo 挂在 Library 实例上,经此客户端读取。
type githubClient struct {
	token    string
	useToken atomic.Bool
	useCLI   atomic.Bool
	auth     *githubAuth
	memo     *githubObjectMemo
}

// newGitHubClient 是构造 GitHub 客户端的唯一入口;预览 / 入库与检查 / 更新路径共用。
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
// 只取各候选 SKILL.md;入库时再按 blob SHA 取所选目录。
func (l *sqliteLibrary) fetchGitHubContents(ctx context.Context, owner, repo string, locator SourceLocator, subpath string) (discoveredSource, error) {
	snap, err := l.fetchGitHubCommitSnapshot(ctx, owner, repo, locator)
	if err != nil {
		return discoveredSource{}, err
	}
	// 预览一定需要目录信息,立即取 tree。
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

// commit 把 ref 解析成 commit 与 root tree。ref 为完整 SHA 时响应不可变,可进 memo;分支 / tag 只合并在途请求。
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

// recursiveTree 取 recursive tree;只有完整(非 truncated)的响应进 memo。
func (c *githubClient) recursiveTree(ctx context.Context, owner, repo, sha string) (githubTreeResponse, error) {
	return memoized(ctx, c.memo, &c.memo.trees, memoKey(memoTree, owner, repo, sha), func() (githubTreeResponse, bool, error) {
		var payload githubTreeResponse
		err := c.get(ctx, githubAPIURL("/repos/%s/%s/git/trees/%s", owner, repo, sha)+"?recursive=1", &payload)
		return payload, err == nil && !payload.Truncated, err
	})
}

// blob 取 blob 正文;解码后按 git blob 规则重算 SHA 与请求的 SHA 对上才算完整,否则视为 API 不可用且不进 memo。
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

// request 复用匿名、环境 token、CLI 的阶梯,仅更高一级成功才记忆明确拒绝。
func (c *githubClient) request(ctx context.Context, httpGet func(string) error, cliGet func() error) error {
	if c.useCLI.Load() {
		return cliGet()
	}
	err := errGitHubContentsFallback
	// anonymousDenied 决定实例是否跳过匿名;httpDenied 是 HTTP 路径最后一级(有 token 则为 token)的结论,决定是否直接走 CLI。
	// 两者都只在更高一级成功后才写回实例:GitHub 对"私有仓库对匿名隐藏"和"资源不存在"同样回 404,
	// 单靠一次被拒分不清,只有下一级真的拿到了同一个对象才证明是前者。
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

// githubAuthDenied 表示这一级被明确拒绝:私有仓库对匿名返回 404,失效 token 返回 401。
// 限流与含义模糊的 403(次级限流、SSO 等)不算,它们不值得让整个实例记住下一级。
func githubAuthDenied(err error) bool {
	httpErr, ok := errors.AsType[*githubHTTPError](err)
	return ok && !httpErr.rateLimited && (httpErr.status == http.StatusUnauthorized || httpErr.status == http.StatusNotFound)
}

// githubCLIGet 使用 gh 自己的登录态,不导出凭证,也不经技能库向调用方透传任何命令输出。
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

// githubContentsUnavailable 表示快路径无法给出完整结果,应回退浅克隆。
func githubContentsUnavailable(err error) bool {
	if errors.Is(err, errGitHubContentsFallback) {
		return true
	}
	_, ok := errors.AsType[*githubHTTPError](err)
	return ok
}

// githubMaterializeError 把入库阶段的 blob 失败映射为技能库的稳定错误码,不把 HTTP 正文或凭证写入 Message。
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

// githubAPIGetOnce 以给定凭证(空为匿名)请求一次 REST 并解析 JSON。
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
	contents, err := v.snap.client.transferBlobs(ctx, v.snap.owner, v.snap.repo, v.snap.ref, 0, []githubBlob{{repoPath: entry.Path, sha: entry.SHA, content: v.snap.storedBlobs[entry.SHA]}})
	if err != nil {
		return nil, err
	}
	return contents[0], nil
}

// readFiles 在同一发现轮次批量传输正文,不改变发现顺序与过滤规则。
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

// githubSourceSnapshot 是一次 GitHub 来源读取:API 路径先只解析 commit,recursive tree 在首次需要目录信息时
// 才取(见 loadTree);克隆回退后已有各 Skill 文件。
type githubSourceSnapshot struct {
	ref          string
	rootTree     string
	entries      []githubTreeEntry          // loadTree 之后才有
	byPath       map[string]githubTreeEntry // nil 表示尚未取 tree
	owner        string
	repo         string
	locator      SourceLocator
	client       *githubClient
	cloned       []discoveredSkill
	discovered   *discoveredSource
	discoveryErr error
	storedBlobs  map[string][]byte // 本次更新已从库读取的正文,不受进程 memo 的容量限制
}

type githubRemoteSkill struct {
	gitTreeSHA  string
	files       []File
	blobs       []githubBlob
	description string
}

// readGitHubSnapshot 对同一 Source 只远程读一次 commit(tree 按需、至多一次;或一次克隆),供检查与更新共用。
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

// fetchGitHubCommitSnapshot 只发一次 commits/{ref} 把 ref 解析成 commit 与 root tree,不取 recursive tree。
func (l *sqliteLibrary) fetchGitHubCommitSnapshot(ctx context.Context, owner, repo string, locator SourceLocator) (githubSourceSnapshot, error) {
	client := l.newGitHubClient()
	commit, rootTree, err := client.commit(ctx, owner, repo, locator.Branch)
	if err != nil {
		return githubSourceSnapshot{}, err
	}
	return githubSourceSnapshot{ref: commit, rootTree: rootTree, owner: owner, repo: repo, client: client}, nil
}

// loadTree 取 recursive tree 并建路径索引;已取过则不再请求。截断的 tree 给不出完整目录,视为 API 不可用。
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

// gitBlobSHA 按 git 对象规则从内容算出 blob SHA-1,供 Update 与远端 tree 比对后复用库中未变文件。
func gitBlobSHA(content []byte) string {
	sum := sha1.New()
	fmt.Fprintf(sum, "blob %d\x00", len(content))
	sum.Write(content)
	return hex.EncodeToString(sum.Sum(nil))
}

// rememberGitBlobs 把当前版本文件按 git blob SHA 写入进程内 memo,发现与取文件都走 client.blob。
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

// confirms 判断远端当前 commit 就是该版本记录的确认 commit:同一 commit 下目录 tree 必然未变,不必取 tree。
func (s *githubSourceSnapshot) confirms(confirmedCommit string) bool {
	return confirmedCommit != "" && s.ref == confirmedCommit
}

func (s *githubSourceSnapshot) locate(ctx context.Context, skill Skill) (githubRemoteSkill, error) {
	if s.cloned == nil && s.discovered == nil {
		// 首次需要目录信息时才取 recursive tree 并发现候选;commit 命中的检查不会走到这里。
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

// githubSkillHasUpdate 先看 commit:与当前版本的确认 commit 相同即无更新;否则取 tree 比较目录 SHA,
// 无 SHA 的旧记录回退为比较文件与可执行位。
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
	return !filesEqual(skill.Files, files), nil
}

// materializeGitHubSkills 在钉住的 commit 上取所选 Skill 的 blob;各 Skill 的 blob 汇成一批并发取回。
// 失败不入库,由调用方保留句柄。
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

// materializeGitHubClone 在钉住的 commit 上按预览时的 subpath 重新克隆发现,再按路径与 tree SHA 匹配所选 Skill。
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

// githubCloneError 保留快路径明确的认证失败,不让克隆不可达把它变成网络错误。
func githubCloneError(ctx context.Context, apiErr, cloneErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if IsCode(cloneErr, SourceUnreachable) && IsCode(githubMaterializeError(apiErr), Permission) {
		return githubMaterializeError(apiErr)
	}
	return cloneErr
}
