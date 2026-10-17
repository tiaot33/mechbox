package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

const defaultTimeout = 5 * time.Minute

var ErrTimeout = errors.New("operation timed out")

var FullCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func timeout() time.Duration {
	parsed, err := time.ParseDuration(os.Getenv("SKILLS_GIT_TIMEOUT"))
	if err != nil || parsed <= 0 {
		return defaultTimeout
	}
	return parsed
}

func Run(ctx context.Context, args ...string) ([]byte, error) {
	ssh, err := sshCommand(ctx)
	if err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout())
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
	KillOnCancel(command)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if commandCtx.Err() != nil {
		return nil, fmt.Errorf("git: %w: %w", ErrTimeout, source.ErrUnreachable)
	}
	if err != nil {
		return nil, commandError(output)
	}
	return output, nil
}

func Remote(ctx context.Context, remotes []string, args []string, patterns ...string) ([]byte, error) {
	var first error
	for i, remote := range remotes {
		command := slices.Clone(args)
		command = append(command, "--", remote)
		command = append(command, patterns...)
		output, err := Run(ctx, command...)
		if err == nil || ctx.Err() != nil {
			return output, err
		}
		if i == 0 {
			first = err
		}
		if i == len(remotes)-1 {
			return nil, Prefer(first, err)
		}
	}
	return nil, errors.New("git: no remote")
}

func Prefer(first, last error) error {
	if errors.Is(first, source.ErrPermission) {
		return first
	}
	return last
}

func ValidateRef(ctx context.Context, ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.Contains(ref, "@{") || strings.ContainsAny(ref, "\x00\r\n") {
		return &source.InputError{Reason: "invalid Git ref"}
	}
	if FullCommit.MatchString(ref) {
		return nil
	}
	if _, err := Run(ctx, "check-ref-format", "refs/heads/"+ref); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &source.InputError{Reason: "invalid Git ref"}
	}
	return nil
}

func MissingRef(err error, ref string) error {
	if missing, ok := errors.AsType[*source.NotFoundError](err); ok && missing.Name == "" {
		missing.Name = ref
	}
	return err
}

func commandError(output []byte) error {
	detail := strings.ToLower(string(output))
	switch {
	case authFailure(detail):
		return fmt.Errorf("git: authentication failed: %w", source.ErrPermission)
	case missingRef(detail):
		return &source.NotFoundError{What: source.MissingRef}
	case unreachable(detail):
		return fmt.Errorf("git: remote could not be reached: %w", source.ErrUnreachable)
	default:
		return fmt.Errorf("git: operation failed; check source, ref and authentication: %w", source.ErrUnreachable)
	}
}

func authFailure(detail string) bool {
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

func missingRef(detail string) bool {
	return strings.Contains(detail, "not found in upstream") ||
		strings.Contains(detail, "couldn't find remote ref") ||
		strings.Contains(detail, "could not find remote ref") ||
		strings.Contains(detail, "could not find remote branch") ||
		strings.Contains(detail, "not our ref") ||
		(strings.Contains(detail, "remote branch") && strings.Contains(detail, "not found"))
}

func unreachable(detail string) bool {
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

func sshCommand(ctx context.Context) (string, error) {
	if command := os.Getenv("GIT_SSH_COMMAND"); command != "" {
		return command, nil
	}
	config := exec.CommandContext(ctx, "git", "config", "--get", "core.sshCommand")
	config.WaitDelay = time.Second
	KillOnCancel(config)
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

func TreeRef(ctx context.Context, remotes []string, tree string) (string, error) {
	output, err := Remote(ctx, remotes, []string{"ls-remote", "--heads", "--tags"})
	if err != nil {
		return "", err
	}
	refs := parseRefs(output)
	matches := treeMatches(tree, append(refs.Branches, refs.Tags...))
	if len(matches) != 1 || strings.Contains(matches[0], "/") {
		return "", &source.InputError{Reason: "tree ref boundary is ambiguous; provide an explicit ref"}
	}
	return matches[0], nil
}

type Refs struct {
	Branches      []string
	Tags          []string
	DefaultBranch string
	InputRef      string
}

func ListRefs(ctx context.Context, remotes []string, ref, tree string) (Refs, error) {
	output, err := Remote(ctx, remotes, []string{"ls-remote", "--symref"}, "HEAD", "refs/heads/*", "refs/tags/*")
	if err != nil {
		return Refs{}, err
	}
	refs := parseRefs(output)
	refs.DefaultBranch, _ = symref(output)
	refs.InputRef = ref
	if ref == "" && tree != "" {
		if matches := treeMatches(tree, append(refs.Branches, refs.Tags...)); len(matches) == 1 {
			refs.InputRef = matches[0]
		}
	}
	if refs.InputRef != "" {
		if err := ValidateRef(ctx, refs.InputRef); err != nil {
			return Refs{}, err
		}
		if FullCommit.MatchString(refs.InputRef) {
			refs.InputRef = strings.ToLower(refs.InputRef)
		}
	}
	return refs, nil
}

func parseRefs(output []byte) Refs {
	refs := Refs{Branches: []string{}, Tags: []string{}}
	for line := range strings.SplitSeq(string(output), "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || strings.HasSuffix(ref, "^{}") {
			continue
		}
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			refs.Branches = append(refs.Branches, branch)
		} else if tag, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
			refs.Tags = append(refs.Tags, tag)
		}
	}
	slices.Sort(refs.Branches)
	slices.Sort(refs.Tags)
	return refs
}

func treeMatches(tree string, refs []string) []string {
	var matches []string
	for _, ref := range refs {
		if (tree == ref || strings.HasPrefix(tree, ref+"/")) && !slices.Contains(matches, ref) {
			matches = append(matches, ref)
		}
	}
	if len(matches) == 0 {
		first, _, _ := strings.Cut(tree, "/")
		if FullCommit.MatchString(first) {
			matches = []string{first}
		}
	}
	return matches
}

func DefaultBranch(ctx context.Context, remotes []string) (string, error) {
	output, err := Remote(ctx, remotes, []string{"ls-remote", "--symref"}, "HEAD")
	if err != nil {
		return "", err
	}
	return symref(output)
}

func symref(output []byte) (string, error) {
	for line := range strings.SplitSeq(string(output), "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			ref, _, _ = strings.Cut(ref, "\t")
			return ref, nil
		}
	}
	return "", fmt.Errorf("remote default branch is unavailable: %w", source.ErrUnreachable)
}

func Head(ctx context.Context, remotes []string, ref string) (string, error) {
	if FullCommit.MatchString(ref) {
		return strings.ToLower(ref), nil
	}
	output, err := Remote(ctx, remotes, []string{"ls-remote"}, "refs/heads/"+ref, "refs/tags/"+ref, "refs/tags/"+ref+"^{}")
	if err != nil {
		return "", err
	}
	found := map[string]string{}
	for line := range strings.SplitSeq(string(output), "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && FullCommit.MatchString(sha) {
			found[name] = strings.ToLower(sha)
		}
	}
	for _, name := range []string{"refs/heads/" + ref, "refs/tags/" + ref + "^{}", "refs/tags/" + ref} {
		if sha, ok := found[name]; ok {
			return sha, nil
		}
	}
	return "", &source.NotFoundError{What: source.MissingRef, Name: ref}
}

func Resolve(ctx context.Context, remotes []string, ref, tree, subpath string) (string, string, error) {
	if tree != "" {
		if ref == "" {
			var err error
			if ref, err = TreeRef(ctx, remotes, tree); err != nil {
				return "", "", err
			}
		}
		suffix := ""
		if tree != ref {
			var ok bool
			suffix, ok = strings.CutPrefix(tree, ref+"/")
			if !ok {
				return "", "", &source.InputError{Reason: "tree URL does not match explicit ref"}
			}
		}
		if subpath != "" && subpath != suffix {
			return "", "", &source.InputError{Reason: "conflicting source subpaths"}
		}
		subpath = suffix
	}
	if ref == "" {
		var err error
		if ref, err = DefaultBranch(ctx, remotes); err != nil {
			return "", "", err
		}
	}
	if err := ValidateRef(ctx, ref); err != nil {
		return "", "", err
	}
	if FullCommit.MatchString(ref) {
		ref = strings.ToLower(ref)
	}
	return ref, subpath, nil
}
