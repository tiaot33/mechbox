package github

import (
	"context"
	"errors"
	"io"
)

type Client struct {
	auth githubAuth
	memo githubObjectMemo
}

func NewClient() *Client {
	return &Client{memo: newGitHubMemo()}
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

var ErrSubpathNotFound = errors.New("source subpath not found")

type RefNotFoundError struct {
	Ref string
}

func (e *RefNotFoundError) Error() string {
	return "git: ref not found"
}

type InputError struct {
	Reason string
}

func (e *InputError) Error() string {
	return e.Reason
}

func invalidInput(reason string) error {
	return &InputError{Reason: reason}
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
