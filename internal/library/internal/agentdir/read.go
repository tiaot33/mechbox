package agentdir

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"syscall"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
)

var (
	ErrIrregularFile = errors.New("irregular file")
	ErrTooLarge      = errors.New("installed folder is too large")
)

func Exists(path string) (bool, error) {
	_, err := on(path).lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, nil
	}
	return false, err
}

func DirectoryExists(path string) (bool, error) {
	info, err := on(path).stat(path)
	if err == nil {
		return info.dir, nil
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, nil
	}
	return false, err
}

type FolderState struct {
	Path       string
	Exists     bool
	Resolves   bool
	Directory  bool
	LinkTarget string
	Files      []skillfile.File
	NoModes    bool
	Modified   time.Time
	Err        error
	key        string
	chain      []hop
}

type hop struct {
	path, key string
}

func (s FolderState) SameEntry(other FolderState) bool {
	return sameKeyedEntry(s.Path, s.key, other.Path, other.key)
}

func (s FolderState) LinksThrough(folders ...FolderState) bool {
	return slices.ContainsFunc(s.chain, func(hop hop) bool {
		return slices.ContainsFunc(folders, func(folder FolderState) bool {
			return sameKeyedEntry(hop.path, hop.key, folder.Path, folder.key)
		})
	})
}

func sameKeyedEntry(a, aKey, b, bKey string) bool {
	if aKey != "" && bKey != "" {
		return aKey == bKey
	}
	return SameEntry(a, b)
}

type limits struct {
	files int
	bytes int64
}

func StatFolders(ctx context.Context, folders []string) ([]FolderState, error) {
	states := make([]FolderState, len(folders))
	err := inspect(ctx, folders, nil, func(index int, state FolderState) error {
		states[index] = state
		return nil
	})
	if err != nil {
		return nil, err
	}
	return states, nil
}

func ReadFolders(ctx context.Context, folders []string, maxFiles int, maxBytes int64, visit func(index int, state FolderState) error) error {
	return inspect(ctx, folders, &limits{files: maxFiles, bytes: maxBytes}, visit)
}

func inspect(ctx context.Context, folders []string, read *limits, visit func(int, FolderState) error) error {
	return bySystem(folders, func(system fileSystem, group []string, indexes []int) error {
		return system.inspect(ctx, group, read, func(i int, state FolderState) error {
			if !system.modes() {
				state.NoModes = true
				for j := range state.Files {
					state.Files[j].Mode = skillfile.ModeRegular
				}
			}
			return visit(indexes[i], state)
		})
	})
}

func bySystem(paths []string, each func(system fileSystem, group []string, indexes []int) error) error {
	groups := map[string][]int{}
	var order []string
	for i, path := range paths {
		key := on(path).key()
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	for _, key := range order {
		indexes := groups[key]
		group := make([]string, len(indexes))
		for i, index := range indexes {
			group[i] = paths[index]
		}
		if err := each(on(group[0]), group, indexes); err != nil {
			return err
		}
	}
	return nil
}

func ReadFolder(ctx context.Context, root string, maxFiles int, maxBytes int64) (FolderState, error) {
	var read FolderState
	err := ReadFolders(ctx, []string{root}, maxFiles, maxBytes, func(_ int, state FolderState) error {
		read = state
		return nil
	})
	return read, err
}

func LinkTarget(path string) string {
	system := on(path)
	info, err := system.lstat(path)
	if err != nil || !info.link {
		return ""
	}
	if resolved, err := system.evalSymlinks(path); err == nil {
		return resolved
	}
	link, err := system.readlink(path)
	if err != nil {
		return ""
	}
	return system.resolveLink(path, link)
}

func LinkChain(path string) []string {
	system := on(path)
	var hops []string
	for range 40 {
		link, err := system.readlink(path)
		if err != nil {
			break
		}
		path = system.resolveLink(path, link)
		hops = append(hops, path)
	}
	return hops
}

func Resolves(path string) bool {
	_, err := on(path).stat(path)
	return err == nil
}

func SameEntry(a, b string) bool {
	if !sameSystem(a, b) {
		return false
	}
	system := on(a)
	if same, known := system.sameEntry(a, b); known {
		return same
	}
	return system.base(a) == system.base(b) && Canonical(system.dir(a)) == Canonical(system.dir(b))
}

func Within(ctx context.Context, directories, targets []string) ([]bool, error) {
	inside := make([]bool, len(targets))
	err := bySystem(targets, func(system fileSystem, group []string, indexes []int) error {
		var same []string
		for _, directory := range directories {
			if on(directory).key() == system.key() {
				same = append(same, directory)
			}
		}
		if len(same) == 0 {
			return nil
		}
		found, err := system.within(ctx, same, group)
		if err != nil {
			return err
		}
		for i, in := range found {
			inside[indexes[i]] = in
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inside, nil
}

func SameFolder(a, b string) bool {
	if !sameSystem(a, b) {
		return false
	}
	if same, known := on(a).sameFolder(a, b); known {
		return same
	}
	return Canonical(a) == Canonical(b)
}

func FoldsCase(path string) bool {
	return on(path).foldsCase(path)
}

func Canonical(path string) string {
	canonical, _ := CanonicalAll(context.Background(), []string{path})
	return canonical[0]
}

func CanonicalAll(ctx context.Context, paths []string) ([]string, error) {
	canonical := make([]string, len(paths))
	err := bySystem(paths, func(system fileSystem, group []string, indexes []int) error {
		resolved, err := system.canonicalAll(ctx, group)
		if err != nil {
			return err
		}
		for i, path := range resolved {
			canonical[indexes[i]] = path
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return canonical, nil
}
