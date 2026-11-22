package github

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/ghproxy"
	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/source/gitcmd"
)

const sourceTimeout = 30 * time.Second

var githubOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
var githubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

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
		if !gitcmd.ValidSubpath(result.tree) {
			return invalid()
		}
	}
	if !gitcmd.ValidSubpath(result.subpath) {
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

func (c *Client) Locate(ctx context.Context, input Input) (source.Target, error) {
	locator, subpath, name, err := resolveGitHubInput(ctx, c.mirror, input)
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

func resolveGitHubInput(ctx context.Context, mirror *ghproxy.Switch, github Input) (locator repoLocator, subpath, name string, err error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return locator, "", "", err
	}
	parsed, err := parseGitHubInput(github)
	if err != nil {
		return locator, "", "", err
	}
	resolved, err := viaMirror(ctx, mirror, parsed.repository, func(ctx context.Context, remotes []string) ([2]string, error) {
		ref, subpath, err := gitcmd.Resolve(ctx, remotes, parsed.ref, parsed.tree, parsed.subpath)
		return [2]string{ref, subpath}, err
	})
	parsed.ref, parsed.subpath = resolved[0], resolved[1]
	if err != nil {
		return locator, "", "", err
	}
	return repoLocator{Repository: parsed.repository, Branch: parsed.ref}, parsed.subpath, parsed.name, nil
}

func (c *Client) InputRefs(ctx context.Context, input string) (gitcmd.Refs, error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	parsed, err := parseGitHubInput(Input{Input: input})
	if err != nil {
		return gitcmd.Refs{}, err
	}
	return viaMirror(ctx, c.mirror, parsed.repository, func(ctx context.Context, remotes []string) (gitcmd.Refs, error) {
		return gitcmd.ListRefs(ctx, remotes, parsed.ref, parsed.tree)
	})
}

func remotes(repository string) []string {
	if repo, ok := strings.CutPrefix(repository, "https://github.com/"); ok {
		return []string{repository, "git@github.com:" + repo}
	}
	return []string{repository}
}

func viaMirror[T any](ctx context.Context, mirror *ghproxy.Switch, repository string, run func(context.Context, []string) (T, error)) (T, error) {
	if got, done, err := tryMirror(ctx, mirror, repository, run); done {
		return got, err
	}
	return run(ctx, remotes(repository))
}

func tryMirror[T any](ctx context.Context, mirror *ghproxy.Switch, repository string, run func(context.Context, []string) (T, error)) (got T, done bool, err error) {
	mirrored, ok := mirror.URL(repository)
	if !ok {
		return got, false, nil
	}
	attempt, cancel := ghproxy.Attempt(ctx)
	defer cancel()
	got, err = run(gitcmd.ThroughMirror(attempt), []string{mirrored})
	if err == nil || ctx.Err() != nil {
		return got, true, err
	}
	if errors.Is(err, source.ErrUnreachable) || errors.Is(err, source.ErrPermission) || errors.Is(err, context.DeadlineExceeded) {
		return got, false, nil
	}
	return got, true, err
}

func Matches(input string) bool {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "git@github.com:") {
		return true
	}
	if before, _, ok := strings.Cut(input, "://"); ok {
		u, err := url.Parse(input)
		return err == nil && before != "" && strings.EqualFold(u.Hostname(), "github.com")
	}
	repository, _, _ := strings.Cut(input, "#")
	owner, _, ok := strings.Cut(repository, "/")
	return ok && !strings.ContainsAny(repository, ":") && !strings.ContainsAny(owner, ".@")
}
