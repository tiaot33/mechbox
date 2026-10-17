package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

var (
	ErrTooLarge     = errors.New("source exceeds read limit")
	ErrNoCandidates = errors.New("no valid Skill candidates")
)

type Entry struct {
	Name string
	Mode fs.FileMode
}

type View interface {
	ReadDir(dir string) ([]Entry, error)
	ReadFile(ctx context.Context, name string) ([]byte, error)
	ReadFiles(ctx context.Context, names []string) ([][]byte, []error)
}

type Candidate struct {
	Name        string
	Description string
	Path        string
	PluginName  string
}

type Diagnostic struct {
	Path   string
	Reason string
}

type abortError struct{ err error }

func (e abortError) Error() string { return e.err.Error() }
func (e abortError) Unwrap() error { return e.err }

func Abort(err error) error {
	return abortError{err}
}

type NoCandidatesError struct {
	Diagnostics []Diagnostic
}

func (e *NoCandidatesError) Error() string {
	reasons := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		reasons[i] = d.Path + ": " + d.Reason
	}
	return fmt.Sprintf("%v: %s", ErrNoCandidates, strings.Join(reasons, "; "))
}

func (e *NoCandidatesError) Unwrap() error {
	return ErrNoCandidates
}

func NoCandidates(diagnostics []Diagnostic) error {
	return &NoCandidatesError{Diagnostics: diagnostics}
}

var excludedDirs = []string{".git", "node_modules", "dist", "build", "__pycache__"}
var priorityDirs = []string{
	"skills", "skills/.curated", "skills/.experimental", "skills/.system",
	".agents/skills", ".claude/skills", ".cline/skills", ".codebuddy/skills", ".codex/skills",
	".commandcode/skills", ".continue/skills", ".factory/skills", ".github/skills", ".goose/skills",
	".grok/skills", ".iflow/skills", ".junie/skills", ".kilo/skills", ".kilocode/skills",
	".kimchi/skills", ".kiro/skills", ".minimax/skills", ".mux/skills", ".neovate/skills",
	".opencode/skills", ".openhands/skills", ".pi/skills", ".posit/assistant/skills", ".qoder/skills",
	".roo/skills", ".trae/skills", ".windsurf/skills", ".zcode/skills", ".zencoder/skills",
}

type step struct {
	dir    string
	file   string
	reason error
}

func Scan(ctx context.Context, view View, root string, load func(candidate Candidate, file string, content []byte) error) ([]Candidate, []Diagnostic, error) {
	candidates := []Candidate{}
	var diagnostics []Diagnostic
	pluginDirs, groups, err := pluginSearchPaths(ctx, view, root)
	if err != nil {
		return nil, nil, err
	}
	processed := map[string]bool{}
	names := map[string]bool{}
	var steps []step
	diagnose := func(dir string, err error) {
		diagnostics = append(diagnostics, Diagnostic{Path: dir, Reason: err.Error()})
	}
	inspect := func(dir string) bool {
		entries, err := readLinkedDir(view, root, dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !processed[dir] {
				steps = append(steps, step{dir: dir, reason: err})
				processed[dir] = true
			}
			return false
		}
		index := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == "SKILL.md" })
		if index < 0 {
			index = slices.IndexFunc(entries, func(e Entry) bool { return strings.EqualFold(e.Name, "SKILL.md") })
		}
		if index < 0 {
			return false
		}
		if processed[dir] {
			return true
		}
		processed[dir] = true
		entry := entries[index]
		if !entry.Mode.IsRegular() {
			steps = append(steps, step{dir: dir, reason: errors.New("SKILL.md must be a regular file")})
			return true
		}
		steps = append(steps, step{dir: dir, file: entry.Name})
		return true
	}
	flush := func() error {
		defer func() { steps = steps[:0] }()
		var paths []string
		for _, s := range steps {
			if s.file != "" {
				paths = append(paths, path.Join(s.dir, s.file))
			}
		}
		contents, readErrs := view.ReadFiles(ctx, paths)
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, err := range readErrs {
			if err != nil && aborts(ctx, err) {
				return err
			}
		}
		index := 0
		for _, s := range steps {
			if s.file == "" {
				diagnose(s.dir, s.reason)
				continue
			}
			i := index
			index++
			if readErrs[i] != nil {
				diagnose(s.dir, readErrs[i])
				continue
			}
			candidate, err := ParseSkillMD(contents[i], s.dir)
			if err != nil {
				diagnose(s.dir, err)
				continue
			}
			if names[candidate.Name] {
				diagnose(s.dir, errors.New("duplicate skill name, first occurrence kept"))
				continue
			}
			candidate.PluginName = groups[s.dir]
			if err := load(candidate, s.file, contents[i]); err != nil {
				if aborts(ctx, err) {
					return err
				}
				diagnose(s.dir, err)
				continue
			}
			names[candidate.Name] = true
			candidates = append(candidates, candidate)
		}
		return nil
	}
	inspect(root)
	if err := flush(); err != nil {
		return nil, nil, err
	}
	if len(candidates) != 0 {
		return candidates, diagnostics, nil
	}
	var walk func(string, int, bool) error
	walk = func(dir string, depth int, fallback bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !fallback {
			for parent := dir; parent != root && parent != "."; parent = path.Dir(parent) {
				if processed[parent] {
					return nil
				}
			}
		}
		entries, err := readLinkedDir(view, root, dir)
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			if !entry.Mode.IsDir() {
				continue
			}
			excluded := slices.Contains(excludedDirs, entry.Name)
			if fallback && excluded {
				continue
			}
			child := path.Join(dir, entry.Name)
			found := inspect(child)
			if depth > 1 && !excluded && (fallback || !found) {
				if err := walk(child, depth-1, fallback); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, 1, false); err != nil {
		return nil, nil, err
	}
	for _, dir := range priorityDirs {
		if err := walk(path.Join(root, dir), 3, false); err != nil {
			return nil, nil, err
		}
	}
	for _, dir := range pluginDirs {
		if err := walk(dir, 1, false); err != nil {
			return nil, nil, err
		}
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	if len(candidates) == 0 {
		if err := walk(root, 5, true); err != nil {
			return nil, nil, err
		}
		if err := flush(); err != nil {
			return nil, nil, err
		}
	}
	return candidates, diagnostics, nil
}

func aborts(ctx context.Context, err error) bool {
	_, aborted := errors.AsType[abortError](err)
	return aborted || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrTooLarge)
}

func readLinkedDir(view View, root, dir string) ([]Entry, error) {
	current := root
	if dir != root {
		rel := strings.TrimPrefix(dir, root+"/")
		if root == "." {
			rel = dir
		}
		for component := range strings.SplitSeq(rel, "/") {
			entries, err := view.ReadDir(current)
			if err != nil {
				return nil, err
			}
			i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == component && e.Mode.IsDir() })
			if i < 0 {
				return nil, fs.ErrNotExist
			}
			current = path.Join(current, component)
		}
	}
	return view.ReadDir(current)
}

func underRoot(p, root string) bool {
	if root == "." {
		return true
	}
	return p == root || strings.HasPrefix(p, root+"/")
}
