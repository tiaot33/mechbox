package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

type Locator []byte

type State []byte

type Status int

const (
	Unknown Status = iota
	Unchanged
	Changed
)

type Snapshot interface {
	Revision() string
	Candidates(ctx context.Context) ([]discovery.Candidate, []discovery.Diagnostic, error)
	Fetch(ctx context.Context, names []string) ([]Skill, error)
	Check(ctx context.Context, have Have) (Status, State, error)
}

type Target struct {
	Type    string
	Locator Locator
	Subpath string
	Names   []string
}

type Description struct {
	Label   string
	Ref     string
	ViewURL string
}

type Kind interface {
	Open(ctx context.Context, loc Locator, subpath string, hint State) (Snapshot, error)
	Describe(loc Locator) (Description, error)
}

type Kinds map[string]Kind

func (k Kinds) Open(ctx context.Context, sourceType string, loc Locator, subpath string, hint State) (Snapshot, error) {
	kind, ok := k[sourceType]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnsupported, sourceType)
	}
	return kind.Open(ctx, loc, subpath, hint)
}

func (k Kinds) Describe(sourceType string, loc Locator) (Description, error) {
	kind, ok := k[sourceType]
	if !ok {
		return Description{}, fmt.Errorf("%w %q", ErrUnsupported, sourceType)
	}
	return kind.Describe(loc)
}

type Skill struct {
	Name        string
	Description string
	Files       []skillfile.File
	State       State
}

type Have struct {
	Name  string
	Files []skillfile.File
	State State
}

var (
	ErrUnreachable = errors.New("source could not be reached")
	ErrPermission  = errors.New("permission denied")
	ErrInvalid     = errors.New("invalid source content")
)

var ErrUnsupported = errors.New("unsupported source type")

type InputError struct {
	Reason string
}

func (e *InputError) Error() string {
	return e.Reason
}

type NotFoundError struct {
	What string
	Name string
}

func (e *NotFoundError) Error() string {
	if e.Name == "" {
		return e.What + " not found"
	}
	return fmt.Sprintf("%s not found: %q", e.What, e.Name)
}
