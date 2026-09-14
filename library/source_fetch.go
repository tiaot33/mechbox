package library

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"go.yaml.in/yaml/v3"
)

const (
	maxFetchSize  = 64 << 20 // 下载与展开的字节上限
	maxFetchFiles = 1000     // 展开文件个数上限;超限拒绝整个归档
)

type discoveredSource struct {
	skills      []fetchedSkill
	diagnostics []CandidateDiagnostic
	ref         string
}

func (l *sqliteLibrary) fetchRemote(ctx context.Context, source Source, subpath string) (discoveredSource, error) {
	switch source.Type {
	case SourceGitHub:
		return l.fetchGitHub(ctx, source.Locator, subpath)
	case SourceURL:
		return fetchArchive(ctx, source.Locator.URL)
	case SourceStore:
		base := os.Getenv("SKILLS_STORE_URL")
		if base == "" {
			return discoveredSource{}, fetchError(SourceUnreachable, "fetch store source", errors.New("SKILLS_STORE_URL is not configured"))
		}
		return fetchArchive(ctx, strings.TrimRight(base, "/")+"/"+url.PathEscape(source.Locator.PackageID)+"/"+url.PathEscape(source.Locator.Version))
	default:
		return discoveredSource{}, &Error{Code: InvalidArgument, Message: "unsupported remote source type"}
	}
}

// fetchGitHub 对 github.com 先走 tree/blob 快路径;API 不可用时回退已加固的浅克隆。
// 预览只取各候选 SKILL.md,安装再按 blob SHA 取所选目录。
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
	return result, githubCloneError(ctx, apiErr, err)
}

// fetchGitHubClone 浅克隆整仓并发现候选;LFS、超时与协议限制由 runSourceGit 提供。
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
		return err
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
	result, err = discoverSkills(ctx, source, current)
	result.ref = strings.TrimSpace(string(output))
	if err != nil {
		return result, err
	}
	for i, skill := range result.skills {
		sha, shaErr := readGitTreeSHA(ctx, repository, skill.preview.Path)
		if shaErr != nil {
			return discoveredSource{}, shaErr
		}
		result.skills[i].gitTreeSHA = sha
	}
	return result, nil
}

// readGitTreeSHA 读取该 Skill 目录的 git tree 对象 SHA;根目录 Skill 用 commit 的 root tree。
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

// fetchArchive 下载 zip;HTTP 超时与搜索/解析相同,不套到 GitHub 克隆上。
func fetchArchive(ctx context.Context, location string) (result discoveredSource, err error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return result, fetchError(SourceUnreachable, "fetch source", errors.New("invalid source URL"))
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fetchError(fetchErrorCode(err), "fetch source", errors.New("source could not be reached"))
	}
	defer response.Body.Close()
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
		return result, fetchError(SourceUnreachable, "read source", errors.New("archive exceeds size limit"))
	}
	result, err = readArchive(ctx, content)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(content)
	result.ref = hex.EncodeToString(sum[:])
	return result, nil
}

func readArchive(ctx context.Context, content []byte) (discoveredSource, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return discoveredSource{}, fetchError(SourceUnreachable, "read source archive", err)
	}
	if len(reader.File) > maxFetchFiles {
		return discoveredSource{}, fetchError(SourceUnreachable, "read source archive", errors.New("archive exceeds file count limit"))
	}
	seen := make(map[string]bool)
	remaining := uint64(maxFetchSize)
	for _, archived := range reader.File {
		if err := ctx.Err(); err != nil {
			return discoveredSource{}, err
		}
		name := strings.TrimSuffix(archived.Name, "/")
		if name == "" || name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) || seen[name] {
			return discoveredSource{}, fetchError(SourceUnreachable, "read source archive", errors.New("unsafe or duplicate path"))
		}
		seen[name] = true
		if archived.UncompressedSize64 > remaining {
			return discoveredSource{}, fetchError(SourceUnreachable, "read source archive", errors.New("expanded content exceeds size limit"))
		}
		remaining -= archived.UncompressedSize64
	}
	return discoverSkills(ctx, reader, ".")
}

func candidateMetadata(content []byte, root string) (FetchSkill, error) {
	result := FetchSkill{Path: root}
	first, rest, ok := strings.Cut(string(content), "\n")
	if !ok || strings.TrimSuffix(first, "\r") != "---" {
		return result, errors.New("missing YAML frontmatter")
	}
	var front strings.Builder
	closed := false
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.TrimSuffix(line, "\r") == "---" {
			closed = true
			break
		}
		front.WriteString(line)
		front.WriteByte('\n')
	}
	if !closed {
		return result, errors.New("unclosed YAML frontmatter")
	}
	var metadata map[string]any
	if err := yaml.Unmarshal([]byte(front.String()), &metadata); err != nil {
		return result, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	name, ok := metadata["name"].(string)
	if !ok || !validSelectionName(name) {
		return result, errors.New("name must be a non-empty safe string")
	}
	result.Name = name
	if description, exists := metadata["description"]; exists {
		result.Description, ok = description.(string)
		if !ok {
			return result, errors.New("description must be a string")
		}
	}
	return result, nil
}

// discoverSkills 只在发现候选后读取完整文件;发现排除目录不会裁剪候选内资源。
func discoverSkills(ctx context.Context, source fs.FS, root string) (result discoveredSource, err error) {
	err = fs.WalkDir(source, root, func(current string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			result.diagnostics = append(result.diagnostics, CandidateDiagnostic{Path: current, Reason: walkErr.Error()})
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if current != root && slices.Contains(discoveryExcludedDirs, entry.Name()) {
			return fs.SkipDir
		}
		entries, err := fs.ReadDir(source, current)
		if err != nil {
			result.diagnostics = append(result.diagnostics, CandidateDiagnostic{Path: current, Reason: err.Error()})
			return fs.SkipDir
		}
		for _, file := range entries {
			if file.Name() != "SKILL.md" {
				continue
			}
			var skill fetchedSkill
			if !file.Type().IsRegular() {
				err = errors.New("SKILL.md must be a regular file")
			} else {
				var content []byte
				content, err = readSourceFile(ctx, source, path.Join(current, "SKILL.md"))
				if err == nil {
					skill.preview, err = candidateMetadata(content, current)
				}
				if err == nil {
					skill.name = skill.preview.Name
					skill.files, err = readCandidateFiles(ctx, source, current)
				}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				result.diagnostics = append(result.diagnostics, CandidateDiagnostic{Path: current, Reason: err.Error()})
			} else {
				result.skills = append(result.skills, skill)
			}
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return discoveredSource{}, err
	}
	return finishDiscovery(result)
}

func readSourceFile(ctx context.Context, source fs.FS, name string) ([]byte, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(contextReader{ctx, file})
	return content, errors.Join(readErr, file.Close())
}

func readCandidateFiles(ctx context.Context, source fs.FS, root string) ([]File, error) {
	var files []File
	err := fs.WalkDir(source, root, func(current string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", current)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content, err := readSourceFile(ctx, source, current)
		if err != nil {
			return err
		}
		mode := ModeRegular
		if info.Mode().Perm()&0o111 != 0 {
			mode = ModeExecutable
		}
		files = append(files, File{Path: strings.TrimPrefix(current, root+"/"), Content: content, Mode: mode})
		return nil
	})
	slices.SortFunc(files, func(a, b File) int { return cmp.Compare(a.Path, b.Path) })
	return files, err
}

func finishDiscovery(result discoveredSource) (discoveredSource, error) {
	names := make(map[string]bool)
	for _, skill := range result.skills {
		if names[skill.name] {
			return discoveredSource{}, &Error{Code: InvalidArgument, Message: fmt.Sprintf("ambiguous skill name %q", skill.name)}
		}
		names[skill.name] = true
	}
	if len(result.skills) == 0 {
		reasons := make([]string, len(result.diagnostics))
		for i, d := range result.diagnostics {
			reasons[i] = d.Path + ": " + d.Reason
		}
		return discoveredSource{}, &Error{Code: InvalidArgument, Message: "no valid Skill candidates: " + strings.Join(reasons, "; ")}
	}
	return result, nil
}
