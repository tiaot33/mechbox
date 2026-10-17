package agentdir

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

var (
	ErrIrregularFile = errors.New("irregular file")
	ErrTooLarge      = errors.New("installed folder is too large")
)

func Exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func DirectoryExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info.IsDir(), nil
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, nil
	}
	return false, err
}

func Read(ctx context.Context, root string) (map[string]skillfile.Digest, error) {
	files := make(map[string]skillfile.Digest)
	err := walk(ctx, root, func(path, relative string, info fs.FileInfo) error {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = skillfile.Digest{Fingerprint: skillfile.Fingerprint(content), Mode: skillfile.ModeOf(info.Mode())}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func ReadFiles(ctx context.Context, root string, maxFiles int, maxBytes int64) ([]skillfile.File, error) {
	var files []skillfile.File
	var size int64
	err := walk(ctx, root, func(path, relative string, info fs.FileInfo) error {
		size += info.Size()
		if len(files) >= maxFiles || size > maxBytes {
			return ErrTooLarge
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if size += int64(len(content)) - info.Size(); size > maxBytes {
			return ErrTooLarge
		}
		files = append(files, skillfile.File{Path: relative, Content: content, Mode: skillfile.ModeOf(info.Mode())})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b skillfile.File) int { return cmp.Compare(a.Path, b.Path) })
	return files, nil
}

func walk(ctx context.Context, root string, visit func(path, relative string, info fs.FileInfo) error) error {
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
		return visit(path, filepath.ToSlash(relative), info)
	})
	if err != nil {
		if errors.Is(err, ErrIrregularFile) || errors.Is(err, ErrTooLarge) {
			return err
		}
		return fmt.Errorf("hash installed files: %w", err)
	}
	return nil
}

func Canonical(path string) string {
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
