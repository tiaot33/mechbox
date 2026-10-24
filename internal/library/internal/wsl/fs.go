package wsl

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	readDirScript = `cd -- "$1" 2>/dev/null || exit 3
for e in * .[!.]* ..?*; do
	[ -e "$e" ] || [ -L "$e" ] || continue
	if [ -L "$e" ]; then t=l; elif [ -d "$e" ]; then t=d; elif [ -f "$e" ]; then t=f; else t=o; fi
	x=0
	s=0
	if [ "$t" = f ]; then
		[ -x "$e" ] && x=1
		s=$(($(wc -c <"$e")))
	fi
	printf '%s\0%s%s\0%s\0' "$e" "$t" "$x" "$s"
done`
	statFileScript = `[ -e "$1" ] || exit 3
if [ -d "$1" ]; then t=d; elif [ -f "$1" ]; then t=f; else t=o; fi
x=0
s=0
if [ "$t" = f ]; then
	[ -x "$1" ] && x=1
	s=$(($(wc -c <"$1")))
fi
printf '%s%s %s' "$t" "$x" "$s"`
	catScript = `[ -e "$1" ] || exit 3
cat -- "$1"`
	tarFilesScript = `cd -- "$1" || exit 3
shift
tar -cf - "$@"`
	batchBytes = 16 << 20
)

func ReadFile(ctx context.Context, distro, linux string) ([]byte, error) {
	return Run(ctx, distro, catScript, linux)
}

type dirFS struct {
	ctx    context.Context
	distro string
	root   string

	mu    sync.Mutex
	dirs  map[string][]fileInfo
	files map[string][]byte
}

func DirFS(ctx context.Context, distro, root string) fs.FS {
	return &dirFS{ctx: ctx, distro: distro, root: root, dirs: map[string][]fileInfo{}, files: map[string][]byte{}}
}

func (f *dirFS) failure(op, name string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		err = fs.ErrNotExist
	}
	return &fs.PathError{Op: op, Path: name, Err: err}
}

func (f *dirFS) Open(name string) (fs.File, error) {
	info, err := f.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return &dirFile{fs: f, name: name, info: info}, nil
	}
	content, err := f.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &regularFile{Reader: bytes.NewReader(content), info: info}, nil
}

func (f *dirFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, f.failure("stat", name, fs.ErrInvalid)
	}
	if name != "." {
		entries, err := f.list(path.Dir(name))
		if err != nil {
			return nil, f.failure("stat", name, err)
		}
		index := slices.IndexFunc(entries, func(entry fileInfo) bool { return entry.name == path.Base(name) })
		if index < 0 {
			return nil, f.failure("stat", name, fs.ErrNotExist)
		}
		if entries[index].mode&fs.ModeSymlink == 0 {
			return entries[index], nil
		}
	}
	output, err := Query(f.ctx, f.distro, statFileScript, path.Join(f.root, name))
	if err != nil {
		return nil, f.failure("stat", name, err)
	}
	kind, size, _ := strings.Cut(output, " ")
	return newInfo(path.Base(name), kind, size), nil
}

func (f *dirFS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, f.failure("open", name, fs.ErrInvalid)
	}
	f.mu.Lock()
	content, ok := f.files[name]
	f.mu.Unlock()
	if ok {
		return bytes.Clone(content), nil
	}
	if err := f.readBatch(name); err != nil {
		return nil, f.failure("open", name, err)
	}
	f.mu.Lock()
	content, ok = f.files[name]
	f.mu.Unlock()
	if ok {
		return bytes.Clone(content), nil
	}
	content, err := ReadFile(f.ctx, f.distro, path.Join(f.root, name))
	if err != nil {
		return nil, f.failure("open", name, err)
	}
	return content, nil
}

func (f *dirFS) readBatch(name string) error {
	dir, base := path.Dir(name), path.Base(name)
	entries, err := f.list(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	index := slices.IndexFunc(entries, func(entry fileInfo) bool { return entry.name == base })
	if index < 0 || !entries[index].mode.IsRegular() {
		return nil
	}
	names := []string{"./" + base}
	total := entries[index].size
	f.mu.Lock()
	for _, entry := range entries {
		_, cached := f.files[path.Join(dir, entry.name)]
		if entry.name == base || cached || !entry.mode.IsRegular() || total+entry.size > batchBytes {
			continue
		}
		names = append(names, "./"+entry.name)
		total += entry.size
	}
	f.mu.Unlock()
	output, err := Run(f.ctx, f.distro, tarFilesScript, append([]string{path.Join(f.root, dir)}, names...)...)
	if err != nil {
		return err
	}
	reader := tar.NewReader(bytes.NewReader(output))
	read := map[string][]byte{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != '\x00' {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		read[path.Join(dir, path.Clean(header.Name))] = content
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for file, content := range read {
		f.files[file] = content
	}
	return nil
}

func (f *dirFS) list(name string) ([]fileInfo, error) {
	f.mu.Lock()
	entries, ok := f.dirs[name]
	f.mu.Unlock()
	if ok {
		return entries, nil
	}
	output, err := Run(f.ctx, f.distro, readDirScript, path.Join(f.root, name))
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	entries = []fileInfo{}
	for i := 0; i+2 < len(fields); i += 3 {
		entries = append(entries, newInfo(fields[i], fields[i+1], fields[i+2]))
	}
	slices.SortFunc(entries, func(a, b fileInfo) int { return strings.Compare(a.name, b.name) })
	f.mu.Lock()
	f.dirs[name] = entries
	f.mu.Unlock()
	return entries, nil
}

func (f *dirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, f.failure("readdir", name, fs.ErrInvalid)
	}
	listed, err := f.list(name)
	if err != nil {
		return nil, f.failure("readdir", name, err)
	}
	entries := make([]fs.DirEntry, len(listed))
	for i, info := range listed {
		entries[i] = fs.FileInfoToDirEntry(info)
	}
	return entries, nil
}

type fileInfo struct {
	name string
	mode fs.FileMode
	size int64
}

func newInfo(name, kind, size string) fileInfo {
	mode := fs.ModeIrregular
	switch kind {
	case "l0":
		mode = fs.ModeSymlink | 0o777
	case "d0":
		mode = fs.ModeDir | 0o755
	case "f0":
		mode = 0o644
	case "f1":
		mode = 0o755
	}
	bytes, _ := strconv.ParseInt(size, 10, 64)
	return fileInfo{name: name, mode: mode, size: bytes}
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return i.size }
func (i fileInfo) Mode() fs.FileMode  { return i.mode }
func (i fileInfo) ModTime() time.Time { return time.Time{} }
func (i fileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fileInfo) Sys() any           { return nil }

type regularFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *regularFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *regularFile) Close() error               { return nil }

type dirFile struct {
	fs      *dirFS
	name    string
	info    fs.FileInfo
	entries []fs.DirEntry
	read    bool
}

func (d *dirFile) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *dirFile) Close() error               { return nil }

func (d *dirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid}
}

func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.read {
		entries, err := d.fs.ReadDir(d.name)
		if err != nil {
			return nil, err
		}
		d.entries, d.read = entries, true
	}
	if n <= 0 {
		entries := d.entries
		d.entries = nil
		return entries, nil
	}
	if len(d.entries) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(d.entries))
	entries := d.entries[:n]
	d.entries = d.entries[n:]
	return entries, nil
}
