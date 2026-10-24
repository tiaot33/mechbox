package agentdir

import (
	"context"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

type fileSystem interface {
	key() string
	join(root, relative string) string
	dir(path string) string
	base(path string) string
	clean(path string) string
	isAbs(path string) bool
	abs(path string) (string, error)
	relative(root, path string) (string, bool)
	resolveLink(path, link string) string

	lstat(path string) (entry, error)
	stat(path string) (entry, error)
	evalSymlinks(path string) (string, error)
	readlink(path string) (string, error)
	readFile(path string) ([]byte, error)
	sameEntry(a, b string) (same, known bool)
	sameFolder(a, b string) (same, known bool)
	within(directories []string) func(resolved string) bool
	canonical(path string) string
	inspect(ctx context.Context, folders []string, read *limits, visit func(int, FolderState) error) error
	list(directory string) ([]Folder, error)
	committed(ctx context.Context, root, directory string) (map[string]bool, error)

	mkdirTemp(directory, prefix string) (string, error)
	writeFiles(stage string, files []skillfile.File) error
	copyTree(ctx context.Context, from, stage string) error
	rename(from, to string) error
	removeTree(path string) error
}

type entry struct {
	dir, link bool
}

func on(path string) fileSystem {
	if name, _, ok := wsl.Split(path); ok {
		return distro{name: name}
	}
	return native{}
}

func sameSystem(a, b string) bool {
	return on(a).key() == on(b).key()
}

func committedNames(output []byte, directory string) map[string]bool {
	prefix := strings.TrimSuffix(directory, "/") + "/"
	committed := map[string]bool{}
	for path := range strings.SplitSeq(string(output), "\x00") {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok || rest == "" {
			continue
		}
		name, _, _ := strings.Cut(rest, "/")
		committed[name] = true
	}
	return committed
}
