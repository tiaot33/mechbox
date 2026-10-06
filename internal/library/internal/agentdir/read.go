package agentdir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

var ErrIrregularFile = errors.New("irregular file")

type Meta struct {
	Fingerprint string
	Mode        skillfile.Mode
}

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
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func Read(ctx context.Context, root string) (map[string]Meta, error) {
	files := make(map[string]Meta)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
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
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = Meta{Fingerprint: skillfile.Fingerprint(content), Mode: skillfile.ModeOf(info.Mode())}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrIrregularFile) {
			return nil, err
		}
		return nil, fmt.Errorf("hash installed files: %w", err)
	}
	return files, nil
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
