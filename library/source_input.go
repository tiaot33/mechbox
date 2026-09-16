package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

const sourceTimeout = 30 * time.Second
const defaultGitTimeout = 5 * time.Minute

var errGitTimeout = errors.New("operation timed out")

// gitTimeout 是 clone/fetch 的独立超时;默认 5 分钟,可用 SKILLS_GIT_TIMEOUT 覆盖。
func gitTimeout() time.Duration {
	parsed, err := time.ParseDuration(os.Getenv("SKILLS_GIT_TIMEOUT"))
	if err != nil || parsed <= 0 {
		return defaultGitTimeout
	}
	return parsed
}

var githubOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
var githubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var fullCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

type githubInput struct {
	repository string
	ref        string
	subpath    string
	name       string
	tree       string
}

// parseGitHubInput 只解释明确的语法;远端 ref 的确认留在 Library 操作中。
func parseGitHubInput(input string, opts ResolveSourceOptions) (result githubInput, err error) {
	invalid := func() (githubInput, error) {
		return githubInput{}, &Error{Code: InvalidArgument, Message: "invalid or ambiguous GitHub source input"}
	}
	input = strings.TrimSpace(input)
	if input == "" || strings.ContainsAny(input, "\x00\r\n\\") {
		return invalid()
	}
	input, fragment, hasRef := strings.Cut(input, "#")
	if hasRef && (fragment == "" || strings.Contains(fragment, "#") || (opts.Ref != "" && opts.Ref != fragment)) {
		return invalid()
	}
	result.ref = cmp.Or(opts.Ref, fragment)
	result.subpath = opts.Subpath
	var repositoryPath string
	if after, ok := strings.CutPrefix(input, "git@github.com:"); ok {
		repositoryPath = after
	} else if strings.Contains(input, "://") {
		u, parseErr := url.Parse(input)
		if parseErr != nil || !strings.EqualFold(u.Host, "github.com") || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "ssh") {
			return invalid()
		}
		if u.Scheme == "https" && u.User != nil {
			return invalid()
		}
		if u.Scheme == "ssh" && u.User != nil && u.User.String() != "git" {
			return invalid()
		}
		repositoryPath = strings.TrimPrefix(u.Path, "/")
	} else {
		repositoryPath = input
	}
	repositoryPath = strings.TrimSuffix(repositoryPath, "/")
	parts := strings.Split(repositoryPath, "/")
	if len(parts) < 2 || !githubOwner.MatchString(parts[0]) {
		return invalid()
	}
	if len(parts) == 2 {
		var selected bool
		parts[1], result.name, selected = strings.Cut(parts[1], "@")
		if selected && !validSelectionName(result.name) {
			return invalid()
		}
	}
	parts[1] = strings.TrimSuffix(parts[1], ".git")
	if !githubRepo.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." {
		return invalid()
	}
	if len(parts) > 2 {
		if len(parts) < 4 || parts[2] != "tree" || result.name != "" {
			return invalid()
		}
		result.tree = strings.Join(parts[3:], "/")
		if !validSubpath(result.tree) {
			return invalid()
		}
	}
	if !validSubpath(result.subpath) {
		return invalid()
	}
	result.repository = "https://github.com/" + strings.ToLower(parts[0]+"/"+parts[1]) + ".git"
	return result, nil
}

func validSubpath(value string) bool {
	return value == "" || (fs.ValidPath(value) && !strings.ContainsAny(value, "\\:\x00") && !strings.Contains("/"+value+"/", "/.git/"))
}

func validSelectionName(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value) && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00\r\n")
}

func (l *sqliteLibrary) ResolveSource(ctx context.Context, input string, opts ResolveSourceOptions) (spec SourceSpec, err error) {
	defer func() { err = typedError(err) }()
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return spec, err
	}
	parsed, err := parseGitHubInput(input, opts)
	if err != nil {
		return spec, err
	}
	if parsed.tree != "" {
		if parsed.ref == "" {
			refs, err := gitRemote(ctx, parsed.repository, "ls-remote", "--heads", "--tags")
			if err != nil {
				return spec, err
			}
			var matches []string
			for line := range strings.SplitSeq(string(refs), "\n") {
				_, ref, ok := strings.Cut(line, "\t")
				if !ok || strings.HasSuffix(ref, "^{}") {
					continue
				}
				ref = strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/tags/")
				if parsed.tree == ref || strings.HasPrefix(parsed.tree, ref+"/") {
					if !slices.Contains(matches, ref) {
						matches = append(matches, ref)
					}
				}
			}
			if len(matches) == 0 {
				first, _, _ := strings.Cut(parsed.tree, "/")
				if fullCommit.MatchString(first) {
					matches = []string{first}
				}
			}
			if len(matches) != 1 || strings.Contains(matches[0], "/") {
				return spec, &Error{Code: InvalidArgument, Message: "tree ref boundary is ambiguous; provide an explicit ref"}
			}
			parsed.ref = matches[0]
		}
		suffix := ""
		if parsed.tree != parsed.ref {
			var ok bool
			suffix, ok = strings.CutPrefix(parsed.tree, parsed.ref+"/")
			if !ok {
				return spec, &Error{Code: InvalidArgument, Message: "tree URL does not match explicit ref"}
			}
		}
		if parsed.subpath != "" && parsed.subpath != suffix {
			return spec, &Error{Code: InvalidArgument, Message: "conflicting source subpaths"}
		}
		parsed.subpath = suffix
	}
	if parsed.ref == "" {
		output, err := gitRemote(ctx, parsed.repository, "ls-remote", "--symref", "HEAD")
		if err != nil {
			return spec, err
		}
		for line := range strings.SplitSeq(string(output), "\n") {
			if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
				parsed.ref, _, _ = strings.Cut(ref, "\t")
				break
			}
		}
		if parsed.ref == "" {
			return spec, &Error{Code: SourceUnreachable, Message: "remote default branch is unavailable"}
		}
	}
	if err := validateGitRef(ctx, parsed.ref); err != nil {
		return spec, err
	}
	if fullCommit.MatchString(parsed.ref) {
		parsed.ref = strings.ToLower(parsed.ref)
	}
	spec = SourceSpec{Type: SourceGitHub, Locator: SourceLocator{Repository: parsed.repository, Branch: parsed.ref}, Subpath: parsed.subpath}
	if parsed.name != "" {
		spec.Names = []string{parsed.name}
	}
	return spec, nil
}

func validateGitRef(ctx context.Context, ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.Contains(ref, "@{") || strings.ContainsAny(ref, "\x00\r\n") {
		return &Error{Code: InvalidArgument, Message: "invalid Git ref"}
	}
	if fullCommit.MatchString(ref) {
		return nil
	}
	if _, err := runSourceGit(ctx, "check-ref-format", "refs/heads/"+ref); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: InvalidArgument, Message: "invalid Git ref"}
	}
	return nil
}

// runSourceGit 使用本机认证配置,禁止凭证提示;关闭 LFS smudge/filter,未安装 git-lfs 时仍可读普通文件。
// 不将可能含凭证的 Git 输出返回调用方。
func runSourceGit(ctx context.Context, args ...string) ([]byte, error) {
	ssh, err := sourceSSHCommand(ctx)
	if err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, gitTimeout())
	defer cancel()
	command := exec.CommandContext(commandCtx, "git", append([]string{
		"-c", "core.askPass=",
		"-c", "credential.interactive=false",
		"-c", "filter.lfs.smudge=",
		"-c", "filter.lfs.process=",
		"-c", "filter.lfs.required=false",
		"-c", "protocol.ext.allow=never",
	}, args...)...)
	// exec.Cmd 对重复键保留最后一次赋值,因此安全相关变量放在调用方环境之后。
	command.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
		"GIT_SSH_COMMAND="+ssh+" -oBatchMode=yes",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_ALLOW_PROTOCOL=https:http:ssh:git:file",
	)
	command.WaitDelay = time.Second
	cancelGitProcess(command)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if commandCtx.Err() != nil {
		return nil, fetchError(SourceUnreachable, "git", errGitTimeout)
	}
	if err != nil {
		return nil, gitCommandError(output)
	}
	return output, nil
}

// missingRefError 给 clone / fetch 阶段报出的 ref 不存在(gitCommandError 唯一的 NotFound 来源)补上 Details.Name。
func missingRefError(err error, ref string) error {
	if typed, ok := errors.AsType[*Error](err); ok && typed.Code == NotFound && typed.Details.Name == "" {
		typed.Details.Name = ref
	}
	return err
}

func gitCommandError(output []byte) error {
	detail := strings.ToLower(string(output))
	switch {
	case gitAuthFailure(detail):
		return fetchError(Permission, "git", errors.New("authentication failed"))
	case gitMissingRef(detail):
		return &Error{Code: NotFound, Message: "git: ref not found"}
	case gitUnreachable(detail):
		return fetchError(SourceUnreachable, "git", errors.New("remote could not be reached"))
	default:
		return fetchError(SourceUnreachable, "git", errors.New("operation failed; check source, ref and authentication"))
	}
}

func gitAuthFailure(detail string) bool {
	return strings.Contains(detail, "authentication") ||
		strings.Contains(detail, "permission denied") ||
		strings.Contains(detail, "access denied") ||
		strings.Contains(detail, "could not read username") ||
		strings.Contains(detail, "could not read password") ||
		strings.Contains(detail, "terminal prompts disabled") ||
		strings.Contains(detail, "error: 403") ||
		strings.Contains(detail, "error: 401") ||
		strings.Contains(detail, "http 403") ||
		strings.Contains(detail, "http 401")
}

func gitMissingRef(detail string) bool {
	return strings.Contains(detail, "not found in upstream") ||
		strings.Contains(detail, "couldn't find remote ref") ||
		strings.Contains(detail, "could not find remote ref") ||
		strings.Contains(detail, "could not find remote branch") ||
		strings.Contains(detail, "not our ref") ||
		(strings.Contains(detail, "remote branch") && strings.Contains(detail, "not found"))
}

func gitUnreachable(detail string) bool {
	return strings.Contains(detail, "could not resolve host") ||
		strings.Contains(detail, "couldn't resolve host") ||
		strings.Contains(detail, "connection refused") ||
		strings.Contains(detail, "couldn't connect") ||
		strings.Contains(detail, "failed to connect") ||
		strings.Contains(detail, "no route to host") ||
		strings.Contains(detail, "network is unreachable") ||
		strings.Contains(detail, "name or service not known") ||
		strings.Contains(detail, "no such host")
}

// sourceSSHCommand 保持 Git 的认证命令优先级;GIT_SSH 是路径,其余值是 shell 命令。
func sourceSSHCommand(ctx context.Context) (string, error) {
	if command := os.Getenv("GIT_SSH_COMMAND"); command != "" {
		return command, nil
	}
	config := exec.CommandContext(ctx, "git", "config", "--get", "core.sshCommand")
	config.WaitDelay = time.Second
	cancelGitProcess(config)
	output, err := config.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err == nil && strings.TrimSpace(string(output)) != "" {
		return strings.TrimSpace(string(output)), nil
	}
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
			return "", fetchError(Internal, "read Git SSH configuration", errors.New("git config failed"))
		}
	}
	if executable := os.Getenv("GIT_SSH"); executable != "" {
		return "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'", nil
	}
	return "ssh", nil
}

// gitRemote 仅在同一 GitHub 仓库上尝试 HTTPS/SSH,不会切换 ref。
func gitRemote(ctx context.Context, repository string, args ...string) ([]byte, error) {
	// ls-remote 的仓库参数位于 HEAD 等筛选参数之前。
	insert := len(args)
	if insert > 0 && args[insert-1] == "HEAD" {
		insert--
	}
	call := func(repository string) ([]byte, error) {
		command := slices.Clone(args[:insert])
		command = append(command, "--", repository)
		command = append(command, args[insert:]...)
		return runSourceGit(ctx, command...)
	}
	output, err := call(repository)
	if err != nil && ctx.Err() == nil {
		if repo, ok := strings.CutPrefix(repository, "https://github.com/"); ok {
			fallback, fallbackErr := call("git@github.com:" + repo)
			if fallbackErr != nil && ctx.Err() == nil {
				return nil, preferGitHubHTTPSError(err, fallbackErr)
			}
			return fallback, fallbackErr
		}
	}
	return output, err
}

// preferGitHubHTTPSError 在 SSH 回退也失败时保留 HTTPS 的认证失败,避免被 SSH 不可达盖掉。
func preferGitHubHTTPSError(httpsErr, sshErr error) error {
	if IsCode(httpsErr, Permission) {
		return httpsErr
	}
	return sshErr
}

func validateSource(source Source) error {
	switch source.Type {
	case SourceGitHub:
		if source.Locator.Repository == "" || source.Locator.Branch == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: github repository and branch are required"}
		}
	case SourceURL:
		if source.Locator.URL == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: url is required"}
		}
	case SourceStore:
		if source.Locator.PackageID == "" || source.Locator.Version == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: store package id and version are required"}
		}
	case SourceLocal:
		if source.Locator.Path == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: local path is required"}
		}
	default:
		return &Error{Code: InvalidArgument, Message: fmt.Sprintf("install skill: unsupported source type %q", source.Type)}
	}
	return nil
}
