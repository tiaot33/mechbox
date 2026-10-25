package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

const Type = "local"

type locator struct {
	Path string `json:"path"`
}

func Matches(input string) bool {
	return wsl.IsPath(input) || filepath.IsAbs(input) || input == "." || input == ".." ||
		strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") ||
		(filepath.Separator == '\\' && (strings.HasPrefix(input, ".\\") || strings.HasPrefix(input, "..\\")))
}

type Kind struct {
	maxFiles int
	maxBytes int64
}

func New(maxFiles int, maxBytes int64) Kind {
	return Kind{maxFiles: maxFiles, maxBytes: maxBytes}
}

func (k Kind) Locate(ctx context.Context, input, home string) (source.Target, error) {
	root, err := absolute(input)
	if err != nil {
		return source.Target{}, fmt.Errorf("resolve source path: %w", err)
	}
	own, err := k.scan(ctx, root)
	if err != nil {
		return source.Target{}, err
	}
	if len(own.skills) == 0 {
		return source.Target{}, discovery.NoCandidates(own.diagnostics)
	}
	target, err := locate(root, own)
	if err != nil {
		return source.Target{}, err
	}
	parent, name, ok := split(root)
	if !ok || len(own.skills) != 1 || own.skills[0].Path != "." {
		return target, nil
	}
	if home != "" {
		if home, err = absolute(home); err != nil || parent == home {
			return target, nil
		}
	}
	siblings, err := k.scan(ctx, parent)
	if ctx.Err() != nil {
		return source.Target{}, ctx.Err()
	}
	if err != nil {
		return target, nil
	}
	for _, skill := range siblings.skills {
		if skill.Path == name {
			found, err := locate(parent, &snapshot{skills: siblings.skills})
			found.Names = []string{skill.Name}
			return found, err
		}
	}
	return target, nil
}

func locate(dir string, snap *snapshot) (source.Target, error) {
	encoded, err := json.Marshal(locator{Path: dir})
	if err != nil {
		return source.Target{}, fmt.Errorf("encode local locator: %w", err)
	}
	return source.Target{Type: Type, Locator: encoded, Snapshot: snap}, nil
}

func split(root string) (parent, name string, ok bool) {
	if distro, linux, ok := wsl.Split(root); ok {
		if linux == "/" {
			return "", "", false
		}
		return wsl.Join(distro, path.Dir(linux)), path.Base(linux), true
	}
	parent = filepath.Dir(root)
	if parent == root {
		return "", "", false
	}
	return parent, filepath.Base(root), true
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
	return k.scan(ctx, decoded.Path)
}

func (k Kind) scan(ctx context.Context, dir string) (*snapshot, error) {
	fsys, info, err := directory(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("read local source: %w", err)
	}
	if !info.IsDir() {
		return nil, &source.InputError{Reason: "read local source: path is not a directory"}
	}
	skills, diagnostics, err := discovery.ScanFS(ctx, fsys, ".", discovery.NewBudget(k.maxFiles, k.maxBytes))
	if err != nil {
		return nil, err
	}
	return &snapshot{skills: skills, diagnostics: diagnostics}, nil
}

func absolute(path string) (string, error) {
	if distro, linux, ok := wsl.Split(path); ok {
		return wsl.Join(distro, linux), nil
	}
	return filepath.Abs(path)
}

func directory(ctx context.Context, path string) (fs.FS, fs.FileInfo, error) {
	if distro, linux, ok := wsl.Split(path); ok {
		fsys := wsl.DirFS(ctx, distro, linux)
		info, err := fs.Stat(fsys, ".")
		return fsys, info, err
	}
	info, err := os.Stat(path)
	return os.DirFS(path), info, err
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
