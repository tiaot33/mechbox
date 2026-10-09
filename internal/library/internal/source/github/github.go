package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

const Type = "github"

type Client struct {
	auth githubAuth
	memo githubObjectMemo
}

func NewClient() *Client {
	return &Client{memo: newGitHubMemo()}
}

func (c *Client) SetToken(token string) {
	if old := c.auth.token.Swap(&token); old != nil && *old == token || old == nil && token == "" {
		return
	}
	c.auth.useToken.Store(false)
	c.auth.useCLI.Store(false)
}

func (c *Client) Close() {
	c.auth.anonymousReset.Store(0)
	c.auth.useToken.Store(false)
	c.auth.useCLI.Store(false)
	c.memo.release()
}

type Input struct {
	Input   string
	Ref     string
	Subpath string
}

type repoLocator struct {
	Repository string `json:"repository,omitempty"`
	Branch     string `json:"branch,omitempty"`
}

func (c *Client) Describe(loc source.Locator) (source.Description, error) {
	locator, err := decodeLocator(loc)
	if err != nil {
		return source.Description{}, err
	}
	description := source.Description{Label: locator.Repository, Ref: locator.Branch}
	if owner, repo, ok := ownerRepo(locator.Repository); ok {
		description.Label = owner + "/" + repo
		description.ViewURL = "https://github.com/" + owner + "/" + repo + "/tree/" + escapeRef(locator.Branch)
	}
	return description, nil
}

func OwnerRepo(sourceType string, loc source.Locator) (owner, repo string, ok bool) {
	if sourceType != Type {
		return "", "", false
	}
	locator, err := decodeLocator(loc)
	if err != nil {
		return "", "", false
	}
	return ownerRepo(locator.Repository)
}

func decodeLocator(loc source.Locator) (repoLocator, error) {
	var locator repoLocator
	if err := json.Unmarshal(loc, &locator); err != nil {
		return repoLocator{}, fmt.Errorf("decode github locator: %w", err)
	}
	return locator, nil
}

func escapeRef(ref string) string {
	segments := strings.Split(ref, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func invalidInput(reason string) error {
	return &source.InputError{Reason: reason}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
