package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/source/download"
	"github.com/tiaot33/mechbox/internal/library/internal/source/git"
	"github.com/tiaot33/mechbox/internal/library/internal/source/gitcmd"
	"github.com/tiaot33/mechbox/internal/library/internal/source/github"
	"github.com/tiaot33/mechbox/internal/library/internal/source/local"
	"github.com/tiaot33/mechbox/internal/library/internal/source/wellknown"
)

type Catalog struct {
	source.Kinds
	github    *github.Client
	download  download.Kind
	wellknown wellknown.Kind
	local     local.Kind
}

func New(httpClient *http.Client, maxFiles int, maxBytes int64) *Catalog {
	client := github.NewClient(httpClient)
	direct := download.New(httpClient, maxFiles, maxBytes)
	known := wellknown.New(httpClient, maxFiles, maxBytes)
	directory := local.New(maxFiles, maxBytes)
	return &Catalog{
		Kinds: source.Kinds{
			github.Type:    client,
			git.Type:       git.Kind{},
			download.Type:  direct,
			wellknown.Type: known,
			local.Type:     directory,
		},
		github:    client,
		download:  direct,
		wellknown: known,
		local:     directory,
	}
}

func (c *Catalog) SetToken(token string) {
	c.github.SetToken(token)
}

func (c *Catalog) Close() {
	c.github.Close()
}

type Input struct {
	Input   string
	Ref     string
	Subpath string
}

func (c *Catalog) Locate(ctx context.Context, in Input, home string) (source.Target, error) {
	input := withHTTPS(in.Input)
	switch {
	case local.Matches(input):
		if err := gitOnly(in); err != nil {
			return source.Target{}, err
		}
		return c.local.Locate(ctx, input, home)
	case download.Hosted(input):
		if err := gitOnly(in); err != nil {
			return source.Target{}, err
		}
		return download.Locate(input)
	case github.Matches(input):
		return github.Locate(ctx, github.Input{Input: input, Ref: in.Ref, Subpath: in.Subpath})
	case git.Matches(input):
		return git.Locate(ctx, git.Input{Input: input, Ref: in.Ref, Subpath: in.Subpath})
	case webLink(input):
		return c.probe(ctx, input, in)
	}
	return source.Target{}, &source.InputError{Reason: "unrecognized source input; expected a local path, a GitHub repository, a Git repository or an http(s) link"}
}

func (c *Catalog) probe(ctx context.Context, link string, in Input) (source.Target, error) {
	link, _, _ = strings.Cut(link, "#")
	if u, err := url.Parse(link); err != nil || u.User != nil {
		return source.Target{}, &source.InputError{Reason: "links with embedded credentials are not supported; configure Git credentials on this machine instead"}
	}
	target, ok, err := c.download.Probe(ctx, link)
	if err != nil {
		return source.Target{}, err
	}
	if !ok {
		target, ok, err = c.wellknown.Locate(ctx, link)
	}
	scope, scoped := errors.AsType[*wellknown.ScopeError](err)
	if err != nil && !scoped {
		return source.Target{}, err
	}
	if ok {
		return target, gitOnly(in)
	}
	target, ok, err = git.Probe(ctx, link, in.Ref, in.Subpath)
	switch {
	case err != nil:
		return source.Target{}, err
	case ok:
		return target, nil
	case scoped:
		return source.Target{}, scope
	}
	return source.Target{}, &source.InputError{Reason: "link is neither a skill file or archive, nor a site with a well-known skills index, nor a Git repository"}
}

func Refs(ctx context.Context, input string) (gitcmd.Refs, error) {
	input = withHTTPS(input)
	switch {
	case local.Matches(input), download.Hosted(input):
		return gitcmd.Refs{}, &source.InputError{Field: "input", Reason: "branches and tags are only listed for Git repositories"}
	case github.Matches(input):
		return github.InputRefs(ctx, input)
	case git.Matches(input):
		return git.InputRefs(ctx, input)
	}
	return gitcmd.Refs{}, &source.InputError{Field: "input", Reason: "branches and tags are only listed for inputs recognizable as Git repositories"}
}

func SearchInputs(origin, name string) (skillInput, sourceInput string, ok bool) {
	if !discovery.ValidName(name) || strings.Contains(name, "#") {
		return "", "", false
	}
	if repository, ok := github.PlainRepository(origin); ok {
		return repository + "@" + name, repository, true
	}
	link, ok := wellknown.SearchInput(origin, name)
	if !ok {
		return "", "", false
	}
	u, err := url.Parse(link)
	if err != nil {
		return "", "", false
	}
	return link, u.Scheme + "://" + u.Host, true
}

func GitHubRepository(sourceType string, loc source.Locator) (owner, repo string, ok bool) {
	return github.OwnerRepo(sourceType, loc)
}

func UserAuthored(sourceType string) bool {
	return sourceType == local.Type
}

func gitOnly(in Input) error {
	switch {
	case in.Ref != "":
		return &source.InputError{Field: "ref", Reason: "ref only applies to Git repositories"}
	case in.Subpath != "":
		return &source.InputError{Field: "subpath", Reason: "subpath only applies to Git repositories"}
	}
	return nil
}

var bareHost = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+(?::[0-9]+)?$`)

func withHTTPS(input string) string {
	host, _, _ := strings.Cut(input, "/")
	host, _, _ = strings.Cut(host, "#")
	if strings.Contains(input, "://") || !bareHost.MatchString(host) {
		return input
	}
	return "https://" + input
}

func webLink(input string) bool {
	u, err := url.Parse(input)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
