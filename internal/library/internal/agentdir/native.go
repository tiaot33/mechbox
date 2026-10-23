package agentdir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
)

type native struct{}

func (native) key() string { return "" }

func (native) join(root, relative string) string {
	return filepath.Join(root, filepath.FromSlash(relative))
}

func (native) dir(path string) string { return filepath.Dir(path) }

func (native) base(path string) string { return filepath.Base(path) }

func (native) clean(path string) string { return filepath.Clean(path) }

func (native) isAbs(path string) bool { return filepath.IsAbs(path) }

func (native) abs(path string) (string, error) { return filepath.Abs(path) }

func (native) relative(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func (native) resolveLink(path, link string) string {
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(path), link)
	}
	return filepath.Clean(link)
}

func (native) lstat(path string) (entry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return entry{}, err
	}
	return entry{dir: info.IsDir(), link: info.Mode()&fs.ModeSymlink != 0, regular: info.Mode().IsRegular()}, nil
}

func (native) stat(path string) (entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return entry{}, err
	}
	return entry{dir: info.IsDir(), regular: info.Mode().IsRegular()}, nil
}

func (native) evalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }

func (native) readlink(path string) (string, error) { return os.Readlink(path) }

func (native) readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (native) sameEntry(a, b string) (bool, bool) {
	aInfo, aErr := os.Lstat(a)
	bInfo, bErr := os.Lstat(b)
	if aErr != nil || bErr != nil {
		return false, false
	}
	return os.SameFile(aInfo, bInfo), true
}

func (native) sameFolder(a, b string) (bool, bool) {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr != nil || bErr != nil {
		return false, false
	}
	return os.SameFile(aInfo, bInfo), true
}

func (native) within(path, directory string) (bool, bool) {
	info, err := os.Stat(directory)
	if err != nil {
		return false, false
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		if parent, err := os.Stat(current); err == nil && os.SameFile(parent, info) {
			return true, true
		}
		if filepath.Dir(current) == current {
			return false, true
		}
	}
}

func (native) canonical(path string) string {
	current, rest := filepath.Clean(path), ""
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Join(current, rest)
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}

func (native) walk(ctx context.Context, root string, visit func(walked) error) error {
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
		return ErrIrregularFile
	} else if err != nil {
		return fmt.Errorf("hash installed files: %w", err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("hash installed files: %w", err)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path != root && skillfile.Ignored(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrIrregularFile
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return visit(walked{
			relative: filepath.ToSlash(relative),
			mode:     info.Mode(),
			size:     info.Size(),
			modTime:  info.ModTime(),
			read:     func() ([]byte, error) { return os.ReadFile(path) },
		})
	})
	if err != nil {
		if errors.Is(err, ErrIrregularFile) || errors.Is(err, ErrTooLarge) {
			return err
		}
		return fmt.Errorf("hash installed files: %w", err)
	}
	return nil
}

func (native) list(directory string) ([]Folder, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list skill folders: %w", err)
	}
	var folders []Folder
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		manifest, err := os.Stat(filepath.Join(path, "SKILL.md"))
		if err != nil || !manifest.Mode().IsRegular() {
			continue
		}
		folder := Folder{Name: entry.Name(), Path: path}
		if entry.Type()&fs.ModeSymlink != 0 {
			if folder.Target, err = filepath.EvalSymlinks(path); err != nil {
				continue
			}
		}
		if manifest.Size() <= maxSkillMD {
			folder.SkillMD, _ = os.ReadFile(filepath.Join(path, "SKILL.md"))
		}
		folders = append(folders, folder)
	}
	return folders, nil
}

func (native) committed(ctx context.Context, root, directory string) (map[string]bool, error) {
	command := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=false", "-C", root, "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", directory)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	return committedNames(output, directory), nil
}

func (native) mkdirTemp(directory, prefix string) (string, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("create scope: %w", err)
	}
	stage, err := os.MkdirTemp(directory, prefix)
	if err != nil {
		return "", fmt.Errorf("stage skill folder: %w", err)
	}
	return stage, nil
}

func (native) writeFiles(stage string, files []skillfile.File) error {
	for _, file := range files {
		if !filepath.IsLocal(file.Path) {
			return fmt.Errorf("invalid skill path: %s", file.Path)
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create skill folder: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == skillfile.ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			return fmt.Errorf("write skill file: %w", err)
		}
	}
	return nil
}

func (native) copyTree(ctx context.Context, from, stage string) error {
	type directoryMode struct {
		path string
		mode fs.FileMode
	}
	var directories []directoryMode
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("copy skill folder: %w", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(stage, relative)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("copy skill folder: %w", err)
		}
		switch {
		case entry.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			directories = append(directories, directoryMode{path: target, mode: info.Mode().Perm()})
			return nil
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			if err := os.WriteFile(target, content, info.Mode().Perm()); err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			return os.Chmod(target, info.Mode().Perm())
		default:
			return fmt.Errorf("copy skill folder: %w: %s", ErrIrregularFile, path)
		}
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return fmt.Errorf("copy skill folder: %w", err)
		}
	}
	return nil
}

func (native) rename(from, to string) error { return os.Rename(from, to) }

func (native) removeTree(path string) error {
	_ = filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil && info.Mode().Perm()&0o700 != 0o700 {
			_ = os.Chmod(current, info.Mode().Perm()|0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}
