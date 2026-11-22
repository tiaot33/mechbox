package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/ghproxy"
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
	kinds     source.Kinds
	withCtx   func(context.Context) context.Context
	github    *github.Client
	download  download.Kind
	wellknown wellknown.Kind
	local     local.Kind
}

func New(httpClient *http.Client, withCtx func(context.Context) context.Context, mirror *ghproxy.Switch, maxFiles int, maxBytes int64) *Catalog {
	client := github.NewClient(httpClient, mirror)
	direct := download.New(mirror.Client(httpClient), maxFiles, maxBytes)
	known := wellknown.New(httpClient, maxFiles, maxBytes)
	directory := local.New(maxFiles, maxBytes)
	return &Catalog{
		kinds: source.Kinds{
			github.Type:    client,
			git.Type:       git.Kind{},
			download.Type:  direct,
			wellknown.Type: known,
			local.Type:     directory,
		},
		github:    client,
		download:  direct,
		wellknown: known,
		withCtx:   withCtx,
		local:     directory,
	}
}

func (c *Catalog) withEnv(ctx context.Context) context.Context {
	if c.withCtx == nil {
		return ctx
	}
	return c.withCtx(ctx)
}

func (c *Catalog) Open(ctx context.Context, sourceType string, loc source.Locator, subpath string, hint source.State) (source.Snapshot, error) {
	snapshot, err := c.kinds.Open(c.withEnv(ctx), sourceType, loc, subpath, hint)
	return c.wrap(snapshot), err
}

func (c *Catalog) Describe(sourceType string, loc source.Locator) (source.Description, error) {
	return c.kinds.Describe(sourceType, loc)
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

type inputKind int

const (
	unrecognized inputKind = iota
	localDirectory
	hostedDownload
	gitHubRepository
	gitRepository
	otherLink
)

func classify(input string) inputKind {
	switch {
	case local.Matches(input):
		return localDirectory
	case download.Hosted(input):
		return hostedDownload
	case github.Matches(input):
		return gitHubRepository
	case git.Matches(input):
		return gitRepository
	case webLink(input):
		return otherLink
	}
	return unrecognized
}

func (c *Catalog) Locate(ctx context.Context, in Input, home string) (source.Target, error) {
	target, err := c.locate(c.withEnv(ctx), in, home)
	target.Snapshot = c.wrap(target.Snapshot)
	return target, err
}

func (c *Catalog) locate(ctx context.Context, in Input, home string) (source.Target, error) {
	input := withHTTPS(in.Input)
	switch classify(input) {
	case localDirectory:
		if err := GitOnly(in); err != nil {
			return source.Target{}, err
		}
		return c.local.Locate(ctx, input, home)
	case hostedDownload:
		if err := GitOnly(in); err != nil {
			return source.Target{}, err
		}
		if err := withoutCredentials(input); err != nil {
			return source.Target{}, err
		}
		return download.Locate(input)
	case gitHubRepository:
		return c.github.Locate(ctx, github.Input{Input: input, Ref: in.Ref, Subpath: in.Subpath})
	case gitRepository:
		return git.Locate(ctx, git.Input{Input: input, Ref: in.Ref, Subpath: in.Subpath})
	case otherLink:
		return c.probe(ctx, input, in)
	}
	return source.Target{}, &source.InputError{Reason: "unrecognized source input; expected a local path, a GitHub repository, a Git repository or an http(s) link"}
}

func withoutCredentials(link string) error {
	if u, err := url.Parse(link); err != nil || u.User != nil {
		return &source.InputError{Reason: "links with embedded credentials are not supported; configure Git credentials on this machine instead"}
	}
	return nil
}

func (c *Catalog) probe(ctx context.Context, link string, in Input) (source.Target, error) {
	link, _, _ = strings.Cut(link, "#")
	if err := withoutCredentials(link); err != nil {
		return source.Target{}, err
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
		return target, GitOnly(in)
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

func (c *Catalog) Refs(ctx context.Context, input string) (gitcmd.Refs, error) {
	ctx = c.withEnv(ctx)
	input = withHTTPS(input)
	switch classify(input) {
	case localDirectory, hostedDownload:
		return gitcmd.Refs{}, &source.InputError{Reason: "branches and tags are only listed for Git repositories"}
	case gitHubRepository:
		return c.github.InputRefs(ctx, input)
	case gitRepository:
		return git.InputRefs(ctx, input)
	}
	return gitcmd.Refs{}, &source.InputError{Reason: "branches and tags are only listed for inputs recognizable as Git repositories"}
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

func GitOnly(in Input) error {
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
