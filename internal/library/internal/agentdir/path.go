package agentdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

var (
	ErrMissing      = errors.New("path does not exist")
	ErrNotDirectory = errors.New("path is not a directory")
)

func Join(root, relative string) string {
	return filepath.Join(root, filepath.FromSlash(relative))
}

func FolderPath(directory, name string) string {
	return filepath.Join(directory, name)
}

func FolderKey(directory, name string) string {
	return filepath.Join(Canonical(directory), name)
}

func FolderName(folder string) string {
	return filepath.Base(folder)
}

func FolderDirectory(folder string) string {
	return filepath.Dir(folder)
}

func Inside(root, path string) bool {
	_, inside := relativeInside(Canonical(root), Canonical(path))
	return inside
}

func Resolve(root, path string) string {
	switch {
	case path == "":
		return ""
	case filepath.IsAbs(path):
		return path
	case root == "":
		return ""
	}
	return Join(root, path)
}

func ProjectRoot(input string) (string, error) {
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "", ErrMissing
	}
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("read project path: %w", err)
	}
	if !info.IsDir() {
		return "", ErrNotDirectory
	}
	return filepath.Clean(resolved), nil
}

func ProjectName(root string) string {
	return filepath.Base(root)
}

func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
