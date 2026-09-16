package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func filesEqual(first, second []File) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i].Path != second[i].Path || first[i].Mode != second[i].Mode || !bytes.Equal(first[i].Content, second[i].Content) {
			return false
		}
	}
	return true
}

func readFiles(ctx context.Context, root string) ([]File, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("read local source: %w", err)
	}
	if !info.IsDir() {
		return nil, argumentError("read local source", "path", "path is not a directory")
	}

	var files []File
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
			return fetchError(SourceInvalid, "read local source", fmt.Errorf("unsupported file type: %s", path))
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		mode := ModeRegular
		if info.Mode().Perm()&0o111 != 0 {
			mode = ModeExecutable
		}
		files = append(files, File{Path: filepath.ToSlash(relative), Content: content, Mode: mode})
		return nil
	})
	if err != nil {
		if _, ok := errors.AsType[*Error](err); ok {
			return nil, err
		}
		return nil, fmt.Errorf("read local source: %w", err)
	}
	return files, nil
}
