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

type SkillNotFoundError struct {
	Name string
}

func (e *SkillNotFoundError) Error() string {
	return fmt.Sprintf("source skill %q not found", e.Name)
}
