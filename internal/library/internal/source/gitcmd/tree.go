package gitcmd

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
)

var (
	errTreeOutput = errors.New("read git tree: unexpected git output")
	errTreePath   = errors.New("read git tree: invalid path")
	errBlobFetch  = errors.New("fetch git blobs")
)

type treeEntry struct {
	mode fs.FileMode
	oid  string
}

type treeView struct {
	repository string
	partial    bool
	root       string
	entries    map[string]treeEntry
	dirs       map[string][]discovery.Entry
	fetched    map[string]bool
}

type treeFile struct {
	path string
	oid  string
	mode skillfile.Mode
}

func readTree(ctx context.Context, repository string, partial bool) (*treeView, error) {
	root, err := Run(ctx, "-C", repository, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return nil, err
	}
	listing, err := runStdout(ctx, nil, "-C", repository, "ls-tree", "-r", "-t", "-z", "--full-tree", "HEAD")
	if err != nil {
		return nil, err
	}
	view := &treeView{
		repository: repository,
		partial:    partial,
		root:       strings.ToLower(strings.TrimSpace(string(root))),
		entries:    map[string]treeEntry{},
		dirs:       map[string][]discovery.Entry{".": {}},
		fetched:    map[string]bool{},
	}
	for record := range strings.SplitSeq(string(listing), "\x00") {
		if record == "" {
			continue
		}
		meta, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, errTreeOutput
		}
		if name == "." || !fs.ValidPath(name) {
			return nil, errTreePath
		}
		var mode fs.FileMode
		switch fields[0] {
		case "040000", "160000":
			mode = fs.ModeDir
			if _, exists := view.dirs[name]; !exists {
				view.dirs[name] = nil
			}
		case "100644":
			mode = 0o644
		case "100755":
			mode = 0o755
		case "120000":
			mode = fs.ModeSymlink
		default:
			mode = fs.ModeIrregular
		}
		view.entries[name] = treeEntry{mode: mode, oid: strings.ToLower(fields[2])}
		parent := path.Dir(name)
		view.dirs[parent] = append(view.dirs[parent], discovery.Entry{Name: path.Base(name), Mode: mode.Type()})
	}
	for dir := range view.dirs {
		slices.SortFunc(view.dirs[dir], func(a, b discovery.Entry) int { return cmp.Compare(a.Name, b.Name) })
	}
	return view, nil
}

func (v *treeView) ReadDir(dir string) ([]discovery.Entry, error) {
	entries, ok := v.dirs[dir]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return entries, nil
}

func (v *treeView) ReadFile(ctx context.Context, name string) ([]byte, error) {
	contents, errs := v.ReadFiles(ctx, []string{name})
	return contents[0], errs[0]
}

func (v *treeView) ReadFiles(ctx context.Context, names []string) ([][]byte, []error) {
	contents, errs := make([][]byte, len(names)), make([]error, len(names))
	var oids []string
	for i, name := range names {
		entry, ok := v.entries[name]
		if !ok || !entry.mode.IsRegular() {
			errs[i] = fs.ErrNotExist
			continue
		}
		oids = append(oids, entry.oid)
	}
	blobs, err := v.blobs(ctx, oids)
	for i, name := range names {
		if errs[i] != nil {
			continue
		}
		if err != nil {
			errs[i] = discovery.Abort(err)
			continue
		}
		contents[i] = blobs[v.entries[name].oid]
	}
	return contents, errs
}

func (v *treeView) treeSHA(dir string) (string, error) {
	sha := v.root
	if dir != "" && dir != "." {
		entry, ok := v.entries[dir]
		if !ok || !entry.mode.IsDir() {
			return "", errors.New("read git tree: skill directory is not a tree")
		}
		sha = entry.oid
	}
	if !FullCommit.MatchString(sha) {
		return "", errors.New("read git tree: invalid skill directory tree SHA")
	}
	return sha, nil
}

func (v *treeView) skillFiles(root string) ([]treeFile, error) {
	var files []treeFile
	var walk func(string) error
	walk = func(dir string) error {
		for _, entry := range v.dirs[dir] {
			if skillfile.Ignored(entry.Name) {
				continue
			}
			current := path.Join(dir, entry.Name)
			switch {
			case entry.Mode.IsDir():
				if err := walk(current); err != nil {
					return err
				}
			case entry.Mode.IsRegular():
				tracked := v.entries[current]
				files = append(files, treeFile{path: strings.TrimPrefix(current, root+"/"), oid: tracked.oid, mode: skillfile.ModeOf(tracked.mode)})
			default:
				return fmt.Errorf("read source: unsupported file type: %s", current)
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b treeFile) int { return cmp.Compare(a.path, b.path) })
	return files, nil
}

func (v *treeView) prefetch(ctx context.Context, root string) error {
	manifests := path.Join(root, ".claude-plugin")
	var oids []string
	for name, entry := range v.entries {
		if !entry.mode.IsRegular() || (root != "." && !strings.HasPrefix(name, root+"/")) {
			continue
		}
		if path.Dir(name) == manifests {
			oids = append(oids, entry.oid)
		}
		if !strings.EqualFold(path.Base(name), "SKILL.md") || discovery.Excluded(name) {
			continue
		}
		oids = append(oids, entry.oid)
		files, err := v.skillFiles(path.Dir(name))
		if err != nil {
			continue
		}
		for _, file := range files {
			oids = append(oids, file.oid)
		}
	}
	return v.fetch(ctx, oids)
}

func (v *treeView) fetch(ctx context.Context, oids []string) error {
	if !v.partial {
		return nil
	}
	var missing []string
	for _, oid := range oids {
		if !v.fetched[oid] {
			missing = append(missing, oid)
		}
	}
	missing = slices.Compact(slices.Sorted(slices.Values(missing)))
	if len(missing) == 0 {
		return nil
	}
	request := strings.Join(missing, "\n") + "\n"
	if _, err := runStdout(ctx, strings.NewReader(request), "-C", v.repository, "-c", "fetch.negotiationAlgorithm=noop",
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no", "--filter=blob:none", "--stdin", "origin"); err != nil {
		return fmt.Errorf("%w: %w", errBlobFetch, err)
	}
	for _, oid := range missing {
		v.fetched[oid] = true
	}
	return nil
}

func (v *treeView) blobs(ctx context.Context, oids []string) (map[string][]byte, error) {
	oids = slices.Compact(slices.Sorted(slices.Values(oids)))
	if len(oids) == 0 {
		return map[string][]byte{}, nil
	}
	if err := v.fetch(ctx, oids); err != nil {
		return nil, err
	}
	batch, err := runStdout(ctx, strings.NewReader(strings.Join(oids, "\n")+"\n"), "-C", v.repository, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	return parseBatch(batch, len(oids))
}

func parseBatch(batch []byte, count int) (map[string][]byte, error) {
	reader := bufio.NewReader(bytes.NewReader(batch))
	blobs := make(map[string][]byte, count)
	for range count {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, errTreeOutput
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[1] != "blob" {
			return nil, errTreeOutput
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 {
			return nil, errTreeOutput
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, errTreeOutput
		}
		if newline, err := reader.ReadByte(); err != nil || newline != '\n' {
			return nil, errTreeOutput
		}
		blobs[strings.ToLower(fields[0])] = content
	}
	return blobs, nil
}
