package source

import (
	"context"
	"fmt"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

type Locator []byte

type Snapshot interface {
	Revision() string
	Candidates() ([]discovery.Candidate, []discovery.Diagnostic)
	Fetch(ctx context.Context, names []string) ([]Skill, error)
	Check(ctx context.Context, have Have) (bool, error)
}

type Skill struct {
	Name        string
	Description string
	Files       []skillfile.File
}

type Have struct {
	Name  string
	Files []skillfile.File
}

type SkillNotFoundError struct {
	Name string
}

func (e *SkillNotFoundError) Error() string {
	return fmt.Sprintf("source skill %q not found", e.Name)
}
