package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

type fileMeta struct {
	fingerprint string
	mode        FileMode
}

var errIrregularFile = errors.New("irregular file")

func hashFiles(ctx context.Context, root string) (map[string]fileMeta, error) {
	files := make(map[string]fileMeta)
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
			return errIrregularFile
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = fileMeta{fingerprint: skillfile.Fingerprint(content), mode: skillfile.ModeOf(info.Mode())}
		return nil
	})
	if err != nil {
		if errors.Is(err, errIrregularFile) {
			return nil, err
		}
		return nil, fmt.Errorf("hash installed files: %w", err)
	}
	return files, nil
}

func canonicalDirectory(path string) string {
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
