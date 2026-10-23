package agentdir

import (
	"cmp"
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
	if errors.Is(err, fs.ErrNotExist) {
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

func Read(ctx context.Context, root string) (map[string]skillfile.Digest, error) {
	files := make(map[string]skillfile.Digest)
	err := on(root).walk(ctx, root, func(file walked) error {
		content, err := file.read()
		if err != nil {
			return err
		}
		files[file.relative] = skillfile.Digest{Fingerprint: skillfile.Fingerprint(content), Mode: skillfile.ModeOf(file.mode)}
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
	err := on(root).walk(ctx, root, func(file walked) error {
		size += file.size
		if len(files) >= maxFiles || size > maxBytes {
			return ErrTooLarge
		}
		content, err := file.read()
		if err != nil {
			return err
		}
		if size += int64(len(content)) - file.size; size > maxBytes {
			return ErrTooLarge
		}
		files = append(files, skillfile.File{Path: file.relative, Content: content, Mode: skillfile.ModeOf(file.mode)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b skillfile.File) int { return cmp.Compare(a.Path, b.Path) })
	return files, nil
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

func Within(path, directory string) bool {
	if !sameSystem(path, directory) {
		return false
	}
	if inside, known := on(directory).within(path, directory); known {
		return inside
	}
	return Inside(directory, path)
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

func Canonical(path string) string {
	return on(path).canonical(path)
}

func Modified(ctx context.Context, root string) (time.Time, error) {
	var latest time.Time
	err := on(root).walk(ctx, root, func(file walked) error {
		if file.modTime.After(latest) {
			latest = file.modTime
		}
		return nil
	})
	return latest, err
}
