package wsl

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

const (
	readDirScript = `cd -- "$1" 2>/dev/null || exit 3
for e in * .[!.]* ..?*; do
	[ -e "$e" ] || [ -L "$e" ] || continue
	if [ -L "$e" ]; then t=l; elif [ -d "$e" ]; then t=d; elif [ -f "$e" ]; then t=f; else t=o; fi
	x=0
	if [ "$t" = f ] && [ -x "$e" ]; then x=1; fi
	printf '%s\0%s%s\0' "$e" "$t" "$x"
done`
	statFileScript = `[ -e "$1" ] || exit 3
if [ -d "$1" ]; then t=d; elif [ -f "$1" ]; then t=f; else t=o; fi
x=0
if [ "$t" = f ] && [ -x "$1" ]; then x=1; fi
printf '%s%s' "$t" "$x"`
	catScript = `[ -e "$1" ] || exit 3
cat -- "$1"`
)

type dirFS struct {
	ctx    context.Context
	distro string
	root   string
}

func DirFS(ctx context.Context, distro, root string) fs.FS {
	return dirFS{ctx: ctx, distro: distro, root: root}
}

func (f dirFS) path(op, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	return path.Join(f.root, name), nil
}

func (f dirFS) failure(op, name string, err error) error {
	if Status(err) == 3 {
		err = fs.ErrNotExist
	}
	return &fs.PathError{Op: op, Path: name, Err: err}
}

func (f dirFS) Open(name string) (fs.File, error) {
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

func (f dirFS) Stat(name string) (fs.FileInfo, error) {
	full, err := f.path("stat", name)
	if err != nil {
		return nil, err
	}
	kind, err := Query(f.ctx, f.distro, statFileScript, full)
	if err != nil {
		return nil, f.failure("stat", name, err)
	}
	return newInfo(path.Base(name), kind), nil
}

func (f dirFS) ReadFile(name string) ([]byte, error) {
	full, err := f.path("open", name)
	if err != nil {
		return nil, err
	}
	content, err := Run(f.ctx, f.distro, catScript, full)
	if err != nil {
		return nil, f.failure("open", name, err)
	}
	return content, nil
}

func (f dirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	full, err := f.path("readdir", name)
	if err != nil {
		return nil, err
	}
	output, err := Run(f.ctx, f.distro, readDirScript, full)
	if err != nil {
		return nil, f.failure("readdir", name, err)
	}
	fields := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	var entries []fs.DirEntry
	for i := 0; i+1 < len(fields); i += 2 {
		entries = append(entries, fs.FileInfoToDirEntry(newInfo(fields[i], fields[i+1])))
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

type fileInfo struct {
	name string
	mode fs.FileMode
}

func newInfo(name, kind string) fileInfo {
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
	return fileInfo{name: name, mode: mode}
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return 0 }
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
	fs      dirFS
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
