package agentdir

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

type Change struct {
	placements []*placement
	removals   []string
	trashed    []trashed
	done       bool
}

type placement struct {
	target    string
	stage     string
	backup    string
	moved     bool
	exclusive bool
}

type TargetExistsError struct {
	Path string
}

func (e *TargetExistsError) Error() string {
	return "target already exists: " + e.Path
}

type trashed struct {
	trash, target string
}

func (c *Change) stage(ctx context.Context, directory, name string) (*placement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create scope: %w", err)
	}
	stage, err := os.MkdirTemp(directory, "."+name+"-")
	if err != nil {
		return nil, fmt.Errorf("stage skill folder: %w", err)
	}
	item := &placement{target: filepath.Join(directory, name), stage: stage}
	c.placements = append(c.placements, item)
	return item, nil
}

func (c *Change) Stage(ctx context.Context, directory, name string, files []skillfile.File) error {
	item, err := c.stage(ctx, directory, name)
	if err != nil {
		return err
	}
	stage := item.stage
	for _, file := range files {
		if !filepath.IsLocal(file.Path) {
			return fmt.Errorf("invalid skill path: %s", file.Path)
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create skill folder: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == skillfile.ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			return fmt.Errorf("write skill file: %w", err)
		}
	}
	return nil
}

func (c *Change) Copy(ctx context.Context, from, directory, name string) error {
	item, err := c.stage(ctx, directory, name)
	if err != nil {
		return err
	}
	item.exclusive = true
	if resolved, err := filepath.EvalSymlinks(from); err == nil {
		from = resolved
	}
	return copyTree(ctx, from, item.stage)
}

func (c *Change) Detach(ctx context.Context, link string) error {
	from, err := filepath.EvalSymlinks(link)
	if err != nil {
		return fmt.Errorf("copy linked folder: %w", err)
	}
	item, err := c.stage(ctx, filepath.Dir(link), filepath.Base(link))
	if err != nil {
		return err
	}
	return copyTree(ctx, from, item.stage)
}

func (c *Change) Targets() []string {
	targets := slices.Clone(c.removals)
	for _, item := range c.placements {
		targets = append(targets, item.target)
	}
	return targets
}

func (c *Change) Touches(path string) bool {
	return slices.ContainsFunc(c.Targets(), func(target string) bool { return SameEntry(target, path) })
}

func copyTree(ctx context.Context, from, stage string) error {
	type directoryMode struct {
		path string
		mode fs.FileMode
	}
	var directories []directoryMode
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("copy skill folder: %w", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(stage, relative)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("copy skill folder: %w", err)
		}
		switch {
		case entry.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			directories = append(directories, directoryMode{path: target, mode: info.Mode().Perm()})
			return nil
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			if err := os.WriteFile(target, content, info.Mode().Perm()); err != nil {
				return fmt.Errorf("copy skill folder: %w", err)
			}
			return os.Chmod(target, info.Mode().Perm())
		default:
			return fmt.Errorf("copy skill folder: %w: %s", ErrIrregularFile, path)
		}
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return fmt.Errorf("copy skill folder: %w", err)
		}
	}
	return nil
}

func (c *Change) Remove(folder string) {
	c.removals = append(c.removals, folder)
}

func (c *Change) Commit(ctx context.Context) error {
	for _, item := range c.placements {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := item.moveIntoPlace(); err != nil {
			return err
		}
	}
	for _, path := range c.removals {
		if err := ctx.Err(); err != nil {
			return err
		}
		trash, err := randomName(filepath.Join(filepath.Dir(path), ".remove-"))
		if err != nil {
			return err
		}
		if err := os.Rename(path, trash); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("remove skill folder: %w", err)
		}
		c.trashed = append(c.trashed, trashed{trash: trash, target: path})
	}
	return ctx.Err()
}

func (p *placement) moveIntoPlace() error {
	if _, err := os.Lstat(p.target); err == nil && p.exclusive {
		return &TargetExistsError{Path: p.target}
	} else if err == nil {
		p.backup = p.stage + ".old"
		if err := os.Rename(p.target, p.backup); err != nil {
			p.backup = ""
			return fmt.Errorf("replace skill folder: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect target folder: %w", err)
	}
	if err := os.Rename(p.stage, p.target); err != nil {
		if p.backup != "" {
			_ = os.Rename(p.backup, p.target)
			p.backup = ""
		}
		return fmt.Errorf("place skill folder: %w", err)
	}
	p.stage = ""
	p.moved = true
	return nil
}

func (c *Change) Rollback() error {
	if c.done {
		return nil
	}
	c.done = true
	var restoreErr error
	for i := len(c.trashed) - 1; i >= 0; i-- {
		restoreErr = errors.Join(restoreErr, os.Rename(c.trashed[i].trash, c.trashed[i].target))
	}
	for i := len(c.placements) - 1; i >= 0; i-- {
		item := c.placements[i]
		if item.moved {
			if err := removeTree(item.target); err != nil {
				restoreErr = errors.Join(restoreErr, err)
			} else if item.backup != "" {
				restoreErr = errors.Join(restoreErr, os.Rename(item.backup, item.target))
			}
		}
		if item.stage != "" {
			restoreErr = errors.Join(restoreErr, removeTree(item.stage))
		}
	}
	return restoreErr
}

func (c *Change) Done() {
	if c.done {
		return
	}
	c.done = true
	for _, item := range c.placements {
		if item.backup != "" {
			_ = removeTree(item.backup)
		}
	}
	for _, entry := range c.trashed {
		_ = removeTree(entry.trash)
	}
}

func removeTree(path string) error {
	_ = filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil && info.Mode().Perm()&0o700 != 0o700 {
			_ = os.Chmod(current, info.Mode().Perm()|0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

func randomName(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}
