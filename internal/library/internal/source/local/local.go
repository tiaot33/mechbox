package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
)

const Type = "local"

type locator struct {
	Path string `json:"path"`
}

func Matches(input string) bool {
	return filepath.IsAbs(input) || input == "." || input == ".." ||
		strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") ||
		(filepath.Separator == '\\' && (strings.HasPrefix(input, ".\\") || strings.HasPrefix(input, "..\\")))
}

func Locate(path string) (source.Target, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return source.Target{}, fmt.Errorf("resolve source path: %w", err)
	}
	encoded, err := json.Marshal(locator{Path: root})
	if err != nil {
		return source.Target{}, fmt.Errorf("encode local locator: %w", err)
	}
	return source.Target{Type: Type, Locator: encoded}, nil
}

type Kind struct {
	maxFiles int
	maxBytes int64
}

func New(maxFiles int, maxBytes int64) Kind {
	return Kind{maxFiles: maxFiles, maxBytes: maxBytes}
}

func (k Kind) Describe(loc source.Locator) (source.Description, error) {
	decoded, err := decode(loc)
	if err != nil {
		return source.Description{}, err
	}
	return source.Description{Label: decoded.Path}, nil
}

func (k Kind) Open(ctx context.Context, loc source.Locator, _ string, _ source.State) (source.Snapshot, error) {
	decoded, err := decode(loc)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(decoded.Path)
	if err != nil {
		return nil, fmt.Errorf("read local source: %w", err)
	}
	if !info.IsDir() {
		return nil, &source.InputError{Reason: "read local source: path is not a directory"}
	}
	skills, diagnostics, err := discovery.ScanFS(ctx, os.DirFS(decoded.Path), ".", discovery.NewBudget(k.maxFiles, k.maxBytes))
	if err != nil {
		return nil, err
	}
	if len(skills) == 0 {
		return nil, discovery.NoCandidates(diagnostics)
	}
	return &snapshot{skills: skills, diagnostics: diagnostics}, nil
}

func decode(loc source.Locator) (locator, error) {
	var decoded locator
	if err := json.Unmarshal(loc, &decoded); err != nil {
		return locator{}, fmt.Errorf("decode local locator: %w", err)
	}
	return decoded, nil
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

func (s *snapshot) Names(context.Context) ([]string, error) {
	names := make([]string, len(s.skills))
	for i, skill := range s.skills {
		names[i] = skill.Name
	}
	return names, nil
}

func (s *snapshot) Commits(context.Context, string) ([]source.Commit, error) {
	return nil, nil
}

func (s *snapshot) Fetch(_ context.Context, names []string) ([]source.Skill, error) {
	for _, name := range names {
		if !slices.ContainsFunc(s.skills, func(skill discovery.Skill) bool { return skill.Name == name }) {
			return nil, &source.NotFoundError{What: source.MissingSkill, Name: name}
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
