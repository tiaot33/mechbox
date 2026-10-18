package discovery

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

type Skill struct {
	Candidate
	Files []skillfile.File
}

type Budget struct {
	maxFiles, files int
	maxBytes, bytes int64
}

func NewBudget(files int, bytes int64) *Budget {
	return &Budget{maxFiles: files, files: files, maxBytes: bytes, bytes: bytes}
}

func (b *Budget) Remaining() (int, int64) {
	return b.files, b.bytes
}

func (b *Budget) Take(files int, size int64) error {
	b.files -= files
	b.bytes -= size
	if b.files < 0 {
		return fmt.Errorf("%w: more than %d files", ErrTooLarge, b.maxFiles)
	}
	if b.bytes < 0 {
		return fmt.Errorf("%w: more than %d bytes", ErrTooLarge, b.maxBytes)
	}
	return nil
}

func ScanFS(ctx context.Context, fsys fs.FS, root string, budget *Budget) ([]Skill, []Diagnostic, error) {
	files := map[string][]skillfile.File{}
	candidates, diagnostics, err := Scan(ctx, fsView{fsys}, root, func(candidate Candidate, _ string, _ []byte) error {
		read, err := readSkillFiles(ctx, fsys, candidate.Path, budget)
		files[candidate.Path] = read
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	skills := make([]Skill, len(candidates))
	for i, candidate := range candidates {
		skills[i] = Skill{Candidate: candidate, Files: files[candidate.Path]}
	}
	return skills, diagnostics, nil
}

type fsView struct{ fsys fs.FS }

func (v fsView) ReadDir(dir string) ([]Entry, error) {
	entries, err := fs.ReadDir(v.fsys, dir)
	result := make([]Entry, len(entries))
	for i, entry := range entries {
		result[i] = Entry{entry.Name(), entry.Type()}
	}
	return result, err
}

func (v fsView) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return fs.ReadFile(v.fsys, name)
}

func (v fsView) ReadFiles(ctx context.Context, names []string) ([][]byte, []error) {
	contents, errs := make([][]byte, len(names)), make([]error, len(names))
	for i, name := range names {
		contents[i], errs[i] = v.ReadFile(ctx, name)
	}
	return contents, errs
}

func readSkillFiles(ctx context.Context, fsys fs.FS, root string, budget *Budget) ([]skillfile.File, error) {
	var files []skillfile.File
	err := fs.WalkDir(fsys, root, func(current string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if current != root && skillfile.Ignored(entry.Name()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("read source: unsupported file type: %s", current)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if budget != nil {
			if err := budget.Take(1, info.Size()); err != nil {
				return err
			}
		}
		content, err := fs.ReadFile(fsys, current)
		if err != nil {
			return err
		}
		if budget != nil && int64(len(content)) > info.Size() {
			if err := budget.Take(0, int64(len(content))-info.Size()); err != nil {
				return err
			}
		}
		files = append(files, skillfile.File{Path: strings.TrimPrefix(current, root+"/"), Content: content, Mode: skillfile.ModeOf(info.Mode())})
		return nil
	})
	slices.SortFunc(files, func(a, b skillfile.File) int { return cmp.Compare(a.Path, b.Path) })
	return files, err
}
