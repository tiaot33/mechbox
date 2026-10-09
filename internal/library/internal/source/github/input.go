package github

import (
	"cmp"
	"context"
	"encoding/json"
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

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

const sourceTimeout = 30 * time.Second
const defaultGitTimeout = 5 * time.Minute

var errGitTimeout = errors.New("operation timed out")

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

func parseGitHubInput(github Input) (result githubInput, err error) {
	invalid := func() (githubInput, error) {
		return githubInput{}, invalidInput("invalid or ambiguous GitHub source input")
	}
	input := strings.TrimSpace(github.Input)
	if input == "" || strings.ContainsAny(input, "\x00\r\n\\") {
		return invalid()
	}
	input, fragment, hasRef := strings.Cut(input, "#")
	if hasRef && (fragment == "" || strings.Contains(fragment, "#") || (github.Ref != "" && github.Ref != fragment)) {
		return invalid()
	}
	result.ref = cmp.Or(github.Ref, fragment)
	result.subpath = github.Subpath
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
		if selected && !discovery.ValidName(result.name) {
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

func PlainRepository(input string) (string, bool) {
	parsed, err := parseGitHubInput(Input{Input: input})
	if err != nil || parsed.tree != "" || parsed.ref != "" || parsed.name != "" {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(parsed.repository, "https://github.com/"), ".git"), true
}

func validSubpath(value string) bool {
	return value == "" || (fs.ValidPath(value) && !strings.ContainsAny(value, "\\:\x00") && !strings.Contains("/"+value+"/", "/.git/"))
}

func Locate(ctx context.Context, input Input) (source.Target, error) {
	locator, subpath, name, err := resolveGitHubInput(ctx, input)
	if err != nil {
		return source.Target{}, err
	}
	encoded, err := json.Marshal(locator)
	if err != nil {
		return source.Target{}, fmt.Errorf("encode github locator: %w", err)
	}
	target := source.Target{Type: Type, Locator: encoded, Subpath: subpath}
	if name != "" {
		target.Names = []string{name}
	}
	return target, nil
}

func resolveGitHubInput(ctx context.Context, github Input) (locator repoLocator, subpath, name string, err error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return locator, "", "", err
	}
	parsed, err := parseGitHubInput(github)
	if err != nil {
		return locator, "", "", err
	}
	if parsed.tree != "" {
		if parsed.ref == "" {
			refs, err := gitRemote(ctx, parsed.repository, "ls-remote", "--heads", "--tags")
			if err != nil {
				return locator, "", "", err
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
				return locator, "", "", invalidInput("tree ref boundary is ambiguous; provide an explicit ref")
			}
			parsed.ref = matches[0]
		}
		suffix := ""
		if parsed.tree != parsed.ref {
			var ok bool
			suffix, ok = strings.CutPrefix(parsed.tree, parsed.ref+"/")
			if !ok {
				return locator, "", "", invalidInput("tree URL does not match explicit ref")
			}
		}
		if parsed.subpath != "" && parsed.subpath != suffix {
			return locator, "", "", invalidInput("conflicting source subpaths")
		}
		parsed.subpath = suffix
	}
	if parsed.ref == "" {
		output, err := gitRemote(ctx, parsed.repository, "ls-remote", "--symref", "HEAD")
		if err != nil {
			return locator, "", "", err
		}
		for line := range strings.SplitSeq(string(output), "\n") {
			if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
				parsed.ref, _, _ = strings.Cut(ref, "\t")
				break
			}
		}
		if parsed.ref == "" {
			return locator, "", "", fmt.Errorf("remote default branch is unavailable: %w", source.ErrUnreachable)
		}
	}
	if err := validateGitRef(ctx, parsed.ref); err != nil {
		return locator, "", "", err
	}
	if fullCommit.MatchString(parsed.ref) {
		parsed.ref = strings.ToLower(parsed.ref)
	}
	return repoLocator{Repository: parsed.repository, Branch: parsed.ref}, parsed.subpath, parsed.name, nil
}

func validateGitRef(ctx context.Context, ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.Contains(ref, "@{") || strings.ContainsAny(ref, "\x00\r\n") {
		return invalidInput("invalid Git ref")
	}
	if fullCommit.MatchString(ref) {
		return nil
	}
	if _, err := runSourceGit(ctx, "check-ref-format", "refs/heads/"+ref); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalidInput("invalid Git ref")
	}
	return nil
}

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
		return nil, fmt.Errorf("git: %w: %w", errGitTimeout, source.ErrUnreachable)
	}
	if err != nil {
		return nil, gitCommandError(output)
	}
	return output, nil
}

func missingRefError(err error, ref string) error {
	if missing, ok := errors.AsType[*source.NotFoundError](err); ok && missing.Name == "" {
		missing.Name = ref
	}
	return err
}

func gitCommandError(output []byte) error {
	detail := strings.ToLower(string(output))
	switch {
	case gitAuthFailure(detail):
		return fmt.Errorf("git: authentication failed: %w", source.ErrPermission)
	case gitMissingRef(detail):
		return &source.NotFoundError{What: source.MissingRef}
	case gitUnreachable(detail):
		return fmt.Errorf("git: remote could not be reached: %w", source.ErrUnreachable)
	default:
		return fmt.Errorf("git: operation failed; check source, ref and authentication: %w", source.ErrUnreachable)
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
			return "", errors.New("read Git SSH configuration: git config failed")
		}
	}
	if executable := os.Getenv("GIT_SSH"); executable != "" {
		return "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'", nil
	}
	return "ssh", nil
}

func gitRemote(ctx context.Context, repository string, args ...string) ([]byte, error) {
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

func preferGitHubHTTPSError(httpsErr, sshErr error) error {
	if errors.Is(httpsErr, source.ErrPermission) {
		return httpsErr
	}
	return sshErr
}
