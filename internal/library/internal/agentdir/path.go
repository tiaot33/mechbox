package agentdir

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
)

var (
	ErrMissing      = errors.New("path does not exist")
	ErrNotDirectory = errors.New("path is not a directory")
)

func Join(root, relative string) string {
	return on(root).join(root, relative)
}

func FolderPath(directory, name string) string {
	return on(directory).join(directory, name)
}

func FolderKey(directory, name string) string {
	return FolderPath(Canonical(directory), name)
}

func FolderName(folder string) string {
	return on(folder).base(folder)
}

func FolderDirectory(folder string) string {
	return on(folder).dir(folder)
}

func Inside(root, path string) bool {
	_, inside := relativeInside(Canonical(root), Canonical(path))
	return inside
}

func Resolve(root, path string) string {
	switch {
	case path == "":
		return ""
	case on(path).isAbs(path):
		return path
	case root == "":
		return ""
	}
	return Join(root, path)
}

func ProjectRoot(input string) (string, error) {
	system := on(input)
	absolute, err := system.abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	resolved, err := system.evalSymlinks(absolute)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "", ErrMissing
	}
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := system.stat(resolved)
	if err != nil {
		return "", fmt.Errorf("read project path: %w", err)
	}
	if !info.dir {
		return "", ErrNotDirectory
	}
	return system.clean(resolved), nil
}

func ProjectName(root string) string {
	return on(root).base(root)
}

func ReadFile(path string) ([]byte, error) {
	return on(path).readFile(path)
}
