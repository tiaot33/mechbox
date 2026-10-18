package git

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/gitcmd"
)

type Input struct {
	Input   string
	Ref     string
	Subpath string
}

type parsedInput struct {
	repository string
	ref        string
	tree       string
	subpath    string
	gitlab     bool
}

func Matches(input string) bool {
	input = strings.TrimSpace(input)
	input, _, _ = strings.Cut(input, "#")
	lower := strings.ToLower(input)
	switch {
	case strings.HasPrefix(lower, "gitlab:"), strings.HasPrefix(lower, "gitlab.com/"), strings.HasPrefix(lower, "ssh://"):
		return true
	case strings.HasPrefix(input, "git@"):
		return strings.Contains(input, ":")
	}
	u, err := url.Parse(input)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), "gitlab.com") || strings.Contains(u.Path, "/-/tree/") || strings.HasSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
}

func invalid() error {
	return &source.InputError{Reason: "invalid or ambiguous Git repository input"}
}

func parse(in Input) (parsedInput, error) {
	input := strings.TrimSpace(in.Input)
	if input == "" || strings.ContainsAny(input, "\x00\r\n\\ ") {
		return parsedInput{}, invalid()
	}
	input, fragment, hasRef := strings.Cut(input, "#")
	if hasRef && (fragment == "" || strings.Contains(fragment, "#") || (in.Ref != "" && in.Ref != fragment)) {
		return parsedInput{}, invalid()
	}
	result := parsedInput{ref: cmp.Or(in.Ref, fragment), subpath: in.Subpath}
	lower := strings.ToLower(input)
	switch {
	case strings.HasPrefix(lower, "gitlab:"):
		input = "https://gitlab.com/" + strings.TrimPrefix(input[len("gitlab:"):], "/")
	case strings.HasPrefix(lower, "gitlab.com/"):
		input = "https://" + input
	}
	if rest, ok := strings.CutPrefix(input, "git@"); ok && !strings.Contains(input, "://") {
		host, repoPath, ok := strings.Cut(rest, ":")
		if !ok || host == "" || strings.ContainsAny(host, "/@") || !validRepoPath(strings.TrimSuffix(repoPath, "/")) {
			return parsedInput{}, invalid()
		}
		result.repository = "git@" + host + ":" + strings.TrimSuffix(repoPath, "/")
		result.gitlab = strings.EqualFold(host, "gitlab.com")
	} else {
		u, err := url.Parse(input)
		if err != nil || u.Host == "" || u.RawQuery != "" || u.Opaque != "" {
			return parsedInput{}, invalid()
		}
		switch u.Scheme {
		case "https", "http":
			if u.User != nil {
				return parsedInput{}, invalid()
			}
		case "ssh":
		default:
			return parsedInput{}, invalid()
		}
		repoPath := strings.Trim(u.Path, "/")
		if before, tree, ok := strings.Cut(repoPath, "/-/tree/"); ok && u.Scheme != "ssh" {
			repoPath, result.tree, result.gitlab = before, strings.Trim(tree, "/"), true
			if !gitcmd.ValidSubpath(result.tree) || result.tree == "" {
				return parsedInput{}, invalid()
			}
		}
		if strings.EqualFold(u.Hostname(), "gitlab.com") {
			result.gitlab = true
		}
		if !validRepoPath(repoPath) || (result.gitlab && strings.Count(strings.TrimSuffix(repoPath, ".git"), "/") < 1) {
			return parsedInput{}, invalid()
		}
		if result.gitlab && u.Scheme != "ssh" {
			repoPath = strings.TrimSuffix(repoPath, ".git") + ".git"
		}
		result.repository = u.Scheme + "://" + u.Host + "/" + repoPath
		if u.User != nil {
			result.repository = u.Scheme + "://" + u.User.String() + "@" + u.Host + "/" + repoPath
		}
	}
	if !gitcmd.ValidSubpath(result.subpath) {
		return parsedInput{}, invalid()
	}
	return result, nil
}

func validRepoPath(repoPath string) bool {
	if repoPath == "" || strings.HasPrefix(repoPath, "-") {
		return false
	}
	for segment := range strings.SplitSeq(repoPath, "/") {
		if segment == "" || segment == "." || segment == ".." || segment == "-" || segment == ".git" {
			return false
		}
	}
	return true
}

func Locate(ctx context.Context, in Input) (source.Target, error) {
	parsed, err := parse(in)
	if err != nil {
		return source.Target{}, err
	}
	loc := locator{Repository: parsed.repository, GitLab: parsed.gitlab}
	loc.Branch, parsed.subpath, err = gitcmd.Resolve(ctx, loc.remotes(), parsed.ref, parsed.tree, parsed.subpath)
	if err != nil {
		return source.Target{}, err
	}
	return target(loc, parsed.subpath)
}

func InputRefs(ctx context.Context, input string) (gitcmd.Refs, error) {
	parsed, err := parse(Input{Input: input})
	if err != nil {
		return gitcmd.Refs{}, err
	}
	loc := locator{Repository: parsed.repository, GitLab: parsed.gitlab}
	return gitcmd.ListRefs(ctx, loc.remotes(), parsed.ref, parsed.tree)
}

func Probe(ctx context.Context, link, ref, subpath string) (source.Target, bool, error) {
	if !gitcmd.ValidSubpath(subpath) {
		return source.Target{}, false, invalid()
	}
	repository := strings.TrimSuffix(link, "/")
	remotes := []string{repository}
	output, err := gitcmd.Remote(ctx, remotes, []string{"ls-remote", "--symref"}, "HEAD")
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, source.ErrPermission) {
			return source.Target{}, false, err
		}
		return source.Target{}, false, nil
	}
	if ref == "" {
		if ref, err = gitcmd.Symref(output); err != nil {
			return source.Target{}, false, err
		}
	}
	loc := locator{Repository: repository}
	if loc.Branch, subpath, err = gitcmd.Resolve(ctx, remotes, ref, "", subpath); err != nil {
		return source.Target{}, false, err
	}
	result, err := target(loc, subpath)
	return result, err == nil, err
}

func target(loc locator, subpath string) (source.Target, error) {
	encoded, err := json.Marshal(loc)
	if err != nil {
		return source.Target{}, fmt.Errorf("encode git locator: %w", err)
	}
	return source.Target{Type: Type, Locator: encoded, Subpath: subpath}, nil
}
