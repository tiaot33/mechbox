package library

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// githubArchiveAllowed 只在 tree 给出全仓文件数和大小时允许整仓下载。
func githubArchiveAllowed(entries []githubTreeEntry) bool {
	count, size := 0, int64(0)
	for _, entry := range entries {
		if entry.Type != "blob" {
			continue
		}
		count++
		if entry.Size == nil || *entry.Size < 0 || *entry.Size > maxFetchSize-size {
			return false
		}
		size += *entry.Size
	}
	return count <= maxFetchFiles
}

// transferBlobs 先复用已有正文与 memo;批量传输只处理尚缺的 SHA,最后逐个 blob 补齐。
func (c *githubClient) transferBlobs(ctx context.Context, owner, repo, commit string, archiveOK bool, blobs []githubBlob) ([][]byte, error) {
	contents := make([][]byte, len(blobs))
	pending := map[string]githubBlob{}
	found := map[string][]byte{}
	for i, blob := range blobs {
		content := blob.content
		if content == nil {
			content, _ = memoGet(c.memo, &c.memo.blobs, memoKey(memoBlob, owner, repo, blob.sha))
		}
		if content != nil {
			contents[i] = content
			found[blob.sha] = content
		} else {
			pending[blob.sha] = blob
		}
	}
	for sha := range found {
		delete(pending, sha)
	}
	if len(pending) > fetchConcurrency && archiveOK {
		got, err := c.tarball(ctx, owner, repo, commit, pending)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			for sha, content := range got {
				found[sha] = content
				delete(pending, sha)
			}
		}
	} else {
		todo := slices.Collect(maps.Values(pending))
		slices.SortFunc(todo, func(a, b githubBlob) int { return strings.Compare(a.repoPath, b.repoPath) })
		results := make([][]byte, len(todo))
		_ = c.request(ctx, func(token string) error {
			errs := make([]error, len(todo))
			err := runConcurrent(ctx, len(todo), func(ctx context.Context, i int) error {
				if results[i] != nil {
					return nil
				}
				blob := todo[i]
				key := memoKey(memoBlob, owner, repo, blob.sha)
				key.raw = true
				results[i], errs[i] = memoized(ctx, c.memo, &c.memo.blobs, key, func() ([]byte, bool, error) {
					location := "https://raw.githubusercontent.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/" + commit + "/" + escapeGitHubPath(blob.repoPath)
					var body bytes.Buffer
					err := githubDownload(ctx, location, token, &body)
					if err == nil && gitBlobSHA(body.Bytes()) != key.id {
						err = errGitHubContentsFallback
					}
					if err != nil {
						return nil, false, err
					}
					content := body.Bytes()
					if content == nil {
						content = []byte{}
					}
					return content, true, nil
				})
				return nil
			})
			if err != nil {
				return err
			}
			// 先处理需要爬阶梯的失败,其他缺失随后逐个回退。
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for i, blob := range todo {
			if results[i] != nil {
				found[blob.sha] = results[i]
				delete(pending, blob.sha)
			}
		}
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
	for i, blob := range blobs {
		if contents[i] == nil {
			contents[i] = found[blob.sha]
		}
	}
	return contents, ctx.Err()
}

func (c *githubClient) tarball(ctx context.Context, owner, repo, commit string, pending map[string]githubBlob) (map[string][]byte, error) {
	file, err := os.CreateTemp("", "skills-github-*.tar.gz")
	if err != nil {
		return nil, errGitHubContentsFallback
	}
	defer os.Remove(file.Name())
	defer file.Close()
	err = c.request(ctx, func(token string) error {
		if err := file.Truncate(0); err != nil {
			return errGitHubContentsFallback
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return errGitHubContentsFallback
		}
		location := "https://codeload.github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/tar.gz/" + commit
		if token != "" {
			location = githubAPIURL("/repos/%s/%s/tarball/%s", owner, repo, commit)
		}
		return githubDownload(ctx, location, token, file)
	}, func() error {
		if err := file.Truncate(0); err != nil {
			return errGitHubContentsFallback
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return errGitHubContentsFallback
		}
		endpoint := "/repos/" + owner + "/" + repo + "/tarball/" + commit
		return githubCLIOutput(ctx, file, "api", endpoint, "--method", "GET", "--hostname", "github.com")
	})
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errGitHubContentsFallback
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, errGitHubContentsFallback
	}
	defer gz.Close()
	// 连 PAX 元数据与 tar 填充也受展开上限约束,避免只统计普通文件而漏掉压缩炸弹。
	expanded := &io.LimitedReader{R: contextReader{ctx, gz}, N: maxFetchSize + 1}
	reader := tar.NewReader(expanded)
	wanted := map[string]string{}
	for sha, blob := range pending {
		wanted[blob.repoPath] = sha
	}
	found := map[string][]byte{}
	seen := map[string]bool{}
	size := int64(0)
	root := ""
	for count := 0; ; count++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count >= maxFetchFiles {
			return nil, errGitHubContentsFallback
		}
		// GitHub 的 git archive 带全局 PAX comment;它是元数据,没有可安装的文件正文。
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) || seen[name] {
			return nil, errGitHubContentsFallback
		}
		seen[name] = true
		prefix, rel, _ := strings.Cut(name, "/")
		if root == "" {
			root = prefix
		}
		if root != prefix {
			return nil, errGitHubContentsFallback
		}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return nil, errGitHubContentsFallback
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || rel == "" || header.Size < 0 || header.Size > maxFetchSize-size {
			return nil, errGitHubContentsFallback
		}
		size += header.Size
		sha, needed := wanted[rel]
		if !needed {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			return nil, errGitHubContentsFallback
		}
		if gitBlobSHA(content) == sha {
			found[sha] = content
		}
	}
	if expanded.N <= 0 {
		return nil, errGitHubContentsFallback
	}
	// 完整归档通过安全校验后才提交 memo,缺文件由调用方逐个补齐。
	for sha, content := range found {
		memoPut(c.memo, &c.memo.blobs, memoKey(memoBlob, owner, repo, sha), content)
	}
	return found, nil
}

const githubGraphQLBatchSize = 100

// graphqlBlobs 每批最多 100 个对象;二进制、截断、编码或 SHA 不符的正文留给 blob 回退。
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
			fmt.Fprintf(&query, " b%d: object(oid:%q) { ... on Blob { text isBinary isTruncated byteSize } }", i, blobs[i].sha)
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
					Text        *string `json:"text"`
					IsBinary    bool    `json:"isBinary"`
					IsTruncated bool    `json:"isTruncated"`
					ByteSize    int     `json:"byteSize"`
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
			if blob == nil || blob.Text == nil || *blob.Text == "" || blob.IsBinary || blob.IsTruncated {
				continue
			}
			content := []byte(*blob.Text)
			if len(content) != blob.ByteSize || gitBlobSHA(content) != blobs[i].sha {
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
