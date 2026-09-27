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
	readFiles(context.Context, []string) ([][]byte, []error)
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

func (v fsSourceView) readFiles(ctx context.Context, names []string) ([][]byte, []error) {
	contents, errs := make([][]byte, len(names)), make([]error, len(names))
	_ = runConcurrent(ctx, len(names), func(ctx context.Context, i int) error {
		contents[i], errs[i] = v.readFile(ctx, names[i])
		if errs[i] != nil && abortsDiscovery(ctx, errs[i]) {
			return errs[i]
		}
		return nil
	})
	return contents, errs
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

// discoveryStep 是遍历按顺序留下的一步:file 非空表示待读取校验的 SKILL.md,否则是目录诊断 reason。
type discoveryStep struct {
	dir    string
	file   string
	reason error
}

// discoverCandidates 负责唯一的发现顺序与有效性规则;load 只补齐有效候选的文件或 blob 定位。
// 遍历只取决于目录结构与 SKILL.md 是否存在,不取决于正文:所以每一轮先按顺序记下步骤,
// 再并发读取本轮的 SKILL.md,最后按原顺序处理,诊断顺序与同名先到先得都与逐个读取一致。
func discoverCandidates(ctx context.Context, view sourceDirectoryView, root string, load func(*fetchedSkill, string, []byte) error) (discoveredSource, error) {
	result := discoveredSource{skills: []fetchedSkill{}}
	pluginDirs, groups, err := pluginSearchPaths(ctx, view, root)
	if err != nil {
		return result, err
	}
	processed := map[string]bool{}
	names := map[string]bool{}
	var steps []discoveryStep
	diagnose := func(dir string, err error) {
		result.diagnostics = append(result.diagnostics, CandidateDiagnostic{Path: dir, Reason: err.Error()})
	}
	inspect := func(dir string) bool {
		entries, err := readLinkedDir(view, root, dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !processed[dir] {
				steps = append(steps, discoveryStep{dir: dir, reason: err})
				processed[dir] = true
			}
			return false
		}
		index := slices.IndexFunc(entries, func(e sourceEntry) bool { return e.name == "SKILL.md" })
		if index < 0 {
			index = slices.IndexFunc(entries, func(e sourceEntry) bool { return strings.EqualFold(e.name, "SKILL.md") })
		}
		if index < 0 {
			return false
		}
		if processed[dir] {
			return true
		}
		processed[dir] = true
		entry := entries[index]
		if !entry.mode.IsRegular() {
			steps = append(steps, discoveryStep{dir: dir, reason: errors.New("SKILL.md must be a regular file")})
			return true
		}
		steps = append(steps, discoveryStep{dir: dir, file: entry.name})
		return true
	}
	// flush 并发读取本轮记下的 SKILL.md,再按顺序处理;中止发现的错误取消其余读取并原样返回。
	flush := func() error {
		defer func() { steps = steps[:0] }()
		var paths []string
		for _, step := range steps {
			if step.file != "" {
				paths = append(paths, path.Join(step.dir, step.file))
			}
		}
		contents, readErrs := view.readFiles(ctx, paths)
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, err := range readErrs {
			if err != nil && abortsDiscovery(ctx, err) {
				return err
			}
		}
		index := 0
		for _, step := range steps {
			if step.file == "" {
				diagnose(step.dir, step.reason)
				continue
			}
			i := index
			index++
			if readErrs[i] != nil {
				diagnose(step.dir, readErrs[i])
				continue
			}
			preview, err := candidateMetadata(contents[i], step.dir)
			if err != nil {
				diagnose(step.dir, err)
				continue
			}
			if names[preview.Name] {
				diagnose(step.dir, errors.New("duplicate skill name, first occurrence kept"))
				continue
			}
			preview.PluginName = groups[step.dir]
			skill := fetchedSkill{name: preview.Name, preview: preview}
			if err := load(&skill, step.file, contents[i]); err != nil {
				if abortsDiscovery(ctx, err) {
					return err
				}
				diagnose(step.dir, err)
				continue
			}
			names[preview.Name] = true
			result.skills = append(result.skills, skill)
		}
		return nil
	}
	inspect(root)
	if err := flush(); err != nil {
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
	if err := flush(); err != nil {
		return result, err
	}
	if len(result.skills) == 0 {
		if err := walk(root, 5, true); err != nil {
			return result, err
		}
		if err := flush(); err != nil {
			return result, err
		}
	}
	return result, nil
}

// abortsDiscovery 表示错误不能记成候选诊断:取消、超时、超出读取上限,或 GitHub 快路径给不出完整结果(应改走克隆)。
func abortsDiscovery(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, errReadLimit) || githubContentsUnavailable(err)
}
