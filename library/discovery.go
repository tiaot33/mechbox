package library

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// sourceDirectoryView 统一 GitHub tree 与 fs.FS 的只读目录访问;发现不读取辅助文件。
type sourceDirectoryView interface {
	readDir(string) ([]sourceEntry, error)
	readFile(context.Context, string) ([]byte, error)
}

type sourceEntry struct {
	name string
	mode fs.FileMode
}

type fsSourceView struct{ source fs.FS }

func (v fsSourceView) readDir(dir string) ([]sourceEntry, error) {
	entries, err := fs.ReadDir(v.source, dir)
	result := make([]sourceEntry, len(entries))
	for i, entry := range entries {
		result[i] = sourceEntry{entry.Name(), entry.Type()}
	}
	return result, err
}

func (v fsSourceView) readFile(ctx context.Context, name string) ([]byte, error) {
	return readSourceFile(ctx, v.source, name)
}

// readLinkedDir 仅沿真实目录访问,避免优先目录或清单路径经 symlink 逃出扫描根。
func readLinkedDir(view sourceDirectoryView, root, dir string) ([]sourceEntry, error) {
	current := root
	if dir != root {
		rel := strings.TrimPrefix(dir, root+"/")
		if root == "." {
			rel = dir
		}
		for component := range strings.SplitSeq(rel, "/") {
			entries, err := view.readDir(current)
			if err != nil {
				return nil, err
			}
			i := slices.IndexFunc(entries, func(e sourceEntry) bool { return e.name == component && e.mode.IsDir() })
			if i < 0 {
				return nil, fs.ErrNotExist
			}
			current = path.Join(current, component)
		}
	}
	return view.readDir(current)
}

var discoveryExcludedDirs = []string{".git", "node_modules", "dist", "build", "__pycache__"}
var discoveryPriorityDirs = []string{
	"skills", "skills/.curated", "skills/.experimental", "skills/.system",
	".agents/skills", ".claude/skills", ".cline/skills", ".codebuddy/skills", ".codex/skills",
	".commandcode/skills", ".continue/skills", ".factory/skills", ".github/skills", ".goose/skills",
	".grok/skills", ".iflow/skills", ".junie/skills", ".kilo/skills", ".kilocode/skills",
	".kimchi/skills", ".kiro/skills", ".minimax/skills", ".mux/skills", ".neovate/skills",
	".opencode/skills", ".openhands/skills", ".pi/skills", ".posit/assistant/skills", ".qoder/skills",
	".roo/skills", ".trae/skills", ".windsurf/skills", ".zcode/skills", ".zencoder/skills",
}

// discoverCandidates 负责唯一的发现顺序与有效性规则;load 只补齐有效候选的文件或 blob 定位。
func discoverCandidates(ctx context.Context, view sourceDirectoryView, root string, load func(*fetchedSkill, string, []byte) error) (discoveredSource, error) {
	result := discoveredSource{skills: []fetchedSkill{}}
	pluginDirs, groups, err := pluginSearchPaths(ctx, view, root)
	if err != nil {
		return result, err
	}
	processed := map[string]bool{}
	names := map[string]bool{}
	diagnose := func(dir string, err error) {
		result.diagnostics = append(result.diagnostics, CandidateDiagnostic{Path: dir, Reason: err.Error()})
	}
	inspect := func(dir string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		entries, err := readLinkedDir(view, root, dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !processed[dir] {
				diagnose(dir, err)
				processed[dir] = true
			}
			return false, nil
		}
		index := slices.IndexFunc(entries, func(e sourceEntry) bool { return e.name == "SKILL.md" })
		if index < 0 {
			index = slices.IndexFunc(entries, func(e sourceEntry) bool { return strings.EqualFold(e.name, "SKILL.md") })
		}
		if index < 0 {
			return false, nil
		}
		if processed[dir] {
			return true, nil
		}
		processed[dir] = true
		entry := entries[index]
		if !entry.mode.IsRegular() {
			diagnose(dir, errors.New("SKILL.md must be a regular file"))
			return true, nil
		}
		content, err := view.readFile(ctx, path.Join(dir, entry.name))
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || githubContentsUnavailable(err) {
				return true, err
			}
			diagnose(dir, err)
			return true, nil
		}
		preview, err := candidateMetadata(content, dir)
		if err != nil {
			diagnose(dir, err)
			return true, nil
		}
		if names[preview.Name] {
			diagnose(dir, errors.New("duplicate skill name, first occurrence kept"))
			return true, nil
		}
		preview.PluginName = groups[dir]
		skill := fetchedSkill{name: preview.Name, preview: preview}
		if err := load(&skill, entry.name, content); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || githubContentsUnavailable(err) {
				return true, err
			}
			diagnose(dir, err)
			return true, nil
		}
		names[preview.Name] = true
		result.skills = append(result.skills, skill)
		return true, nil
	}
	if _, err := inspect(root); err != nil {
		return result, err
	}
	if len(result.skills) != 0 {
		return result, nil
	}
	var walk func(string, int, bool) error
	walk = func(dir string, depth int, fallback bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// 优先项重叠时,不进入已经识别为 Skill 的目录内部。
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
			if !entry.mode.IsDir() {
				continue
			}
			excluded := slices.Contains(discoveryExcludedDirs, entry.name)
			if fallback && excluded {
				continue
			}
			child := path.Join(dir, entry.name)
			found, err := inspect(child)
			if err != nil {
				return err
			}
			if depth > 1 && !excluded && (fallback || !found) {
				if err := walk(child, depth-1, fallback); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, 1, false); err != nil {
		return result, err
	}
	for _, dir := range discoveryPriorityDirs {
		if err := walk(path.Join(root, dir), 3, false); err != nil {
			return result, err
		}
	}
	for _, dir := range pluginDirs {
		if err := walk(dir, 1, false); err != nil {
			return result, err
		}
	}
	if len(result.skills) == 0 {
		if err := walk(root, 5, true); err != nil {
			return result, err
		}
	}
	return result, nil
}
