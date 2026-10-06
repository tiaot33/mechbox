package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

var ErrNotDirectory = errors.New("path is not a directory")

type locator struct {
	Path string `json:"path"`
}

func Open(ctx context.Context, loc source.Locator, maxFiles int, maxBytes int64) (source.Snapshot, error) {
	var decoded locator
	if err := json.Unmarshal(loc, &decoded); err != nil {
		return nil, fmt.Errorf("decode local locator: %w", err)
	}
	info, err := os.Stat(decoded.Path)
	if err != nil {
		return nil, fmt.Errorf("read local source: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrNotDirectory
	}
	skills, diagnostics, err := discovery.ScanFS(ctx, os.DirFS(decoded.Path), ".", discovery.NewBudget(maxFiles, maxBytes))
	if err != nil {
		return nil, err
	}
	if len(skills) == 0 {
		return nil, discovery.NoCandidates(diagnostics)
	}
	return &snapshot{skills: skills, diagnostics: diagnostics}, nil
}

type snapshot struct {
	skills      []discovery.Skill
	diagnostics []discovery.Diagnostic
}

func (s *snapshot) Revision() string {
	return ""
}

func (s *snapshot) Candidates(context.Context) ([]discovery.Candidate, []discovery.Diagnostic, error) {
	candidates := make([]discovery.Candidate, len(s.skills))
	for i, skill := range s.skills {
		candidates[i] = skill.Candidate
	}
	return candidates, s.diagnostics, nil
}

func (s *snapshot) Fetch(_ context.Context, names []string) ([]source.Skill, error) {
	for _, name := range names {
		if !slices.ContainsFunc(s.skills, func(skill discovery.Skill) bool { return skill.Name == name }) {
			return nil, &source.SkillNotFoundError{Name: name}
		}
	}
	var result []source.Skill
	for _, skill := range s.skills {
		if slices.Contains(names, skill.Name) {
			result = append(result, source.Skill{Name: skill.Name, Description: skill.Description, Files: skill.Files})
		}
	}
	return result, nil
}

func (s *snapshot) Check(ctx context.Context, have source.Have) (source.Status, source.State, error) {
	if _, err := s.Fetch(ctx, []string{have.Name}); err != nil {
		return source.Unknown, nil, err
	}
	return source.Unknown, nil, nil
}
