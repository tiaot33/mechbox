package library

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

func escapeGitHubPath(name string) string {
	parts := strings.Split(name, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// githubDownload 只把凭证发往初始受信地址;所有重定向都移除 Authorization。
func githubDownload(ctx context.Context, location, token string, dest io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return errGitHubContentsFallback
	}
	if req.URL.Host == githubAPIHost {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	req.Header.Set("User-Agent", "skills-tui")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := *http.DefaultClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Authorization")
		if len(via) >= 10 {
			return errGitHubContentsFallback
		}
		return nil
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errGitHubContentsFallback
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// 限流响应恒带 Reset;解析失败存 0 即不避让。
		reset, _ := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64)
		return &githubHTTPError{status: response.StatusCode, rateLimited: githubRateLimited(response), reset: reset}
	}
	n, err := io.Copy(dest, io.LimitReader(contextReader{ctx, response.Body}, maxFetchSize+1))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || n > maxFetchSize {
		return errGitHubContentsFallback
	}
	return nil
}

const (
	githubArchiveSmallRepo = 4 << 20 // 整仓不超过此值时,一次下载总比多轮往返划算
	githubArchiveMaxWaste  = 4       // 否则整仓至多是待取字节的这么多倍才下载归档
)

// githubRepoSize 返回 tree 中全部文件的总字节数;任一条目缺少大小或总量超过下载上限时返回 0,不走整仓下载。
func githubRepoSize(entries []githubTreeEntry) int64 {
	size := int64(0)
	for _, entry := range entries {
		if entry.Type != "blob" {
			continue
		}
		if entry.Size == nil || *entry.Size < 0 || *entry.Size > maxFetchSize-size {
			return 0
		}
		size += *entry.Size
	}
	return size
}

func githubEntrySize(entry githubTreeEntry) int64 {
	if entry.Size == nil {
		return 0
	}
	return *entry.Size
}

// githubArchiveWorth 判断整仓归档是否比逐个取划算:待取文件超过一个并发批次,
// 且整仓足够小,或下载的无关字节不超过待取字节的若干倍。
func githubArchiveWorth(repoSize int64, pending map[string]githubBlob) bool {
	if repoSize <= 0 || len(pending) <= fetchConcurrency {
		return false
	}
	if repoSize <= githubArchiveSmallRepo {
		return true
	}
	wanted := int64(0)
	for _, blob := range pending {
		wanted += blob.size
	}
	return wanted*githubArchiveMaxWaste >= repoSize
}

// transferBlobs 按 blobs 顺序返回正文:先用已有正文与 memo,划算时取一次整仓归档(repoSize 为 0 表示不取),
// 剩下的走 raw(只有 gh 时走 GraphQL),最后逐个 blob 补齐。每一级取回的正文都已按 blob SHA 校验。
func (c *githubClient) transferBlobs(ctx context.Context, owner, repo, commit string, repoSize int64, blobs []githubBlob) ([][]byte, error) {
	found := map[string][]byte{}
	pending := map[string]githubBlob{}
	for _, blob := range blobs {
		content := blob.content
		if content == nil {
			content, _ = memoGet(c.memo, &c.memo.blobs, memoKey(memoBlob, owner, repo, blob.sha))
		}
		if content != nil {
			found[blob.sha] = content
		} else {
			pending[blob.sha] = blob
		}
	}
	take := func(got map[string][]byte) {
		for sha, content := range got {
			found[sha] = content
			delete(pending, sha)
		}
	}
	// 同一 SHA 可能出现在多个路径,其中一处已有正文即可。
	for sha := range found {
		delete(pending, sha)
	}
	if githubArchiveWorth(repoSize, pending) {
		take(c.tarball(ctx, owner, repo, commit, pending))
	}
	if len(pending) > 0 && ctx.Err() == nil {
		take(c.rawBlobs(ctx, owner, repo, commit, pending))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	todo := slices.Collect(maps.Values(pending))
	results := make([][]byte, len(todo))
	if err := runConcurrent(ctx, len(todo), func(ctx context.Context, i int) error {
		var err error
		results[i], err = c.blob(ctx, owner, repo, todo[i].sha)
		return err
	}); err != nil {
		return nil, err
	}
	for i, blob := range todo {
		found[blob.sha] = results[i]
	}
	contents := make([][]byte, len(blobs))
	for i, blob := range blobs {
		contents[i] = found[blob.sha]
	}
	return contents, nil
}

// rawBlobs 按 commit 从 raw 有界并发取正文,只有 gh 时改用 GraphQL 批量取;取不到的留给调用方逐个回退。
func (c *githubClient) rawBlobs(ctx context.Context, owner, repo, commit string, pending map[string]githubBlob) map[string][]byte {
	todo := slices.Collect(maps.Values(pending))
	slices.SortFunc(todo, func(a, b githubBlob) int { return strings.Compare(a.repoPath, b.repoPath) })
	results := make([][]byte, len(todo))
	_ = c.request(ctx, func(token string) error {
		errs := make([]error, len(todo))
		if err := runConcurrent(ctx, len(todo), func(ctx context.Context, i int) error {
			if results[i] == nil {
				results[i], errs[i] = c.rawBlob(ctx, owner, repo, commit, todo[i], token)
			}
			return nil
		}); err != nil {
			return err
		}
		// 先返回需要爬阶梯的失败,其他缺失随后逐个回退。
		for _, err := range errs {
			if isGitHubAuthRetry(err) {
				return err
			}
		}
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	}, func() error { return c.graphqlBlobs(ctx, owner, repo, todo, results) })
	got := map[string][]byte{}
	for i, blob := range todo {
		if results[i] != nil {
			got[blob.sha] = results[i]
		}
	}
	return got
}

func (c *githubClient) rawBlob(ctx context.Context, owner, repo, commit string, blob githubBlob, token string) ([]byte, error) {
	location := "https://raw.githubusercontent.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/" + commit + "/" + escapeGitHubPath(blob.repoPath)
	var body bytes.Buffer
	if err := githubDownload(ctx, location, token, &body); err != nil {
		return nil, err
	}
	content := body.Bytes()
	if gitBlobSHA(content) != blob.sha {
		return nil, errGitHubContentsFallback
	}
	if content == nil {
		content = []byte{}
	}
	memoPut(c.memo, &c.memo.blobs, memoKey(memoBlob, owner, repo, blob.sha), content)
	return content, nil
}

// tarball 下载 commit 的整仓归档,只取出 pending 里 SHA 相符的文件。归档不落盘,路径只用于查找,
// 正文以 tree 的 blob SHA 为准,所以无关条目、symlink、异常路径一律跳过;
// 归档损坏或超限时保留已校验的部分,缺失的由调用方回退。
func (c *githubClient) tarball(ctx context.Context, owner, repo, commit string, pending map[string]githubBlob) map[string][]byte {
	var archive bytes.Buffer
	err := c.request(ctx, func(token string) error {
		archive.Reset()
		location := "https://codeload.github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/tar.gz/" + commit
		if token != "" {
			location = githubAPIURL("/repos/%s/%s/tarball/%s", owner, repo, commit)
		}
		return githubDownload(ctx, location, token, &archive)
	}, func() error {
		archive.Reset()
		endpoint := "/repos/" + owner + "/" + repo + "/tarball/" + commit
		return githubCLIOutput(ctx, &archive, "api", endpoint, "--method", "GET", "--hostname", "github.com")
	})
	if err != nil {
		return nil
	}
	gz, err := gzip.NewReader(&archive)
	if err != nil {
		return nil
	}
	defer gz.Close()
	// 展开字节上限连 tar 头与填充一起算,防压缩炸弹。
	reader := tar.NewReader(io.LimitReader(contextReader{ctx, gz}, maxFetchSize))
	wanted := make(map[string]string, len(pending))
	for sha, blob := range pending {
		wanted[blob.repoPath] = sha
	}
	found := map[string][]byte{}
	for len(found) < len(wanted) {
		header, err := reader.Next()
		if err != nil {
			break
		}
		// 顶层目录是 {repo}-{sha}/,去掉后即 tree 路径。
		_, rel, _ := strings.Cut(header.Name, "/")
		sha, ok := wanted[rel]
		if !ok || header.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			break
		}
		if gitBlobSHA(content) == sha {
			found[sha] = content
			memoPut(c.memo, &c.memo.blobs, memoKey(memoBlob, owner, repo, sha), content)
		}
	}
	return found
}

const githubGraphQLBatchSize = 100

// graphqlBlobs 每批最多 100 个对象。二进制 blob 的 text 为 null,截断或编码转换过的文本 SHA 对不上,都留给 blob 回退。
func (c *githubClient) graphqlBlobs(ctx context.Context, owner, repo string, blobs []githubBlob, contents [][]byte) error {
	var missing []int
	for i := range blobs {
		if contents[i] == nil {
			missing = append(missing, i)
		}
	}
	for batch := range slices.Chunk(missing, githubGraphQLBatchSize) {
		var query strings.Builder
		fmt.Fprintf(&query, "query { repository(owner:%q, name:%q) {", owner, repo)
		for _, i := range batch {
			fmt.Fprintf(&query, " b%d: object(oid:%q) { ... on Blob { text } }", i, blobs[i].sha)
		}
		query.WriteString(" } }")
		var body bytes.Buffer
		err := githubCLIOutput(ctx, &body, "api", "graphql", "--hostname", "github.com", "-f", "query="+query.String())
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// GraphQL 可以同时返回 data 与 errors,只接受可独立校验的对象。
		var payload struct {
			Data struct {
				Repository map[string]*struct {
					Text *string `json:"text"`
				} `json:"repository"`
			} `json:"data"`
		}
		if json.Unmarshal(body.Bytes(), &payload) != nil || payload.Data.Repository == nil {
			if err != nil {
				return err
			}
			return errGitHubContentsFallback
		}
		for _, i := range batch {
			blob := payload.Data.Repository[fmt.Sprintf("b%d", i)]
			if blob == nil || blob.Text == nil {
				continue
			}
			content := []byte(*blob.Text)
			if gitBlobSHA(content) != blobs[i].sha {
				continue
			}
			contents[i] = content
			memoPut(c.memo, &c.memo.blobs, memoKey(memoBlob, owner, repo, blobs[i].sha), content)
		}
	}
	// 有效的部分结果仍可复用;缺失或不可信对象不能算作鉴权成功。
	for _, i := range missing {
		if contents[i] == nil {
			return errGitHubContentsFallback
		}
	}
	return nil
}

// githubCLIOutput 输出直接写到有界 writer;超限立即取消整个进程组,stderr 从不进入错误。
func githubCLIOutput(ctx context.Context, dest io.Writer, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, "gh", args...)
	command.Env = append(os.Environ(), "GITHUB_TOKEN=", "GH_TOKEN=", "GH_PROMPT_DISABLED=1")
	command.WaitDelay = time.Second
	cancelGitProcess(command)
	command.Stdout = &githubLimitedWriter{dest: dest, remaining: maxFetchSize, cancel: cancel}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errGitHubContentsFallback
	}
	return nil
}

type githubLimitedWriter struct {
	dest      io.Writer
	remaining int
	cancel    context.CancelFunc
}

func (w *githubLimitedWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		w.cancel()
		return 0, errGitHubContentsFallback
	}
	n, err := w.dest.Write(p)
	w.remaining -= n
	return n, err
}
