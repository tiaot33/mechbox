package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		mode := ModeRegular
		if info.Mode().Perm()&0o111 != 0 {
			mode = ModeExecutable
		}
		sum := sha256.Sum256(content)
		files[filepath.ToSlash(relative)] = fileMeta{fingerprint: hex.EncodeToString(sum[:]), mode: mode}
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
