package agentdir

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
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
	stage, err := on(directory).mkdirTemp(directory, "."+name+"-")
	if err != nil {
		return nil, err
	}
	item := &placement{target: FolderPath(directory, name), stage: stage}
	c.placements = append(c.placements, item)
	return item, nil
}

func (c *Change) Stage(ctx context.Context, directory, name string, files []skillfile.File) error {
	item, err := c.stage(ctx, directory, name)
	if err != nil {
		return err
	}
	return on(item.stage).writeFiles(item.stage, files)
}

func (c *Change) Copy(ctx context.Context, from, directory, name string) error {
	item, err := c.stage(ctx, directory, name)
	if err != nil {
		return err
	}
	item.exclusive = true
	if resolved, err := on(from).evalSymlinks(from); err == nil {
		from = resolved
	}
	return copyTree(ctx, from, item.stage)
}

func (c *Change) Detach(ctx context.Context, link string) error {
	from, err := on(link).evalSymlinks(link)
	if err != nil {
		return fmt.Errorf("copy linked folder: %w", err)
	}
	item, err := c.stage(ctx, FolderDirectory(link), FolderName(link))
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

func copyTree(ctx context.Context, from, stage string) error {
	if !sameSystem(from, stage) {
		return fmt.Errorf("copy skill folder: %s and %s are on different systems", from, stage)
	}
	return on(stage).copyTree(ctx, from, stage)
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
		trash, err := randomName(FolderPath(FolderDirectory(path), ".remove-"))
		if err != nil {
			return err
		}
		if err := on(path).rename(path, trash); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("remove skill folder: %w", err)
		}
		c.trashed = append(c.trashed, trashed{trash: trash, target: path})
	}
	return ctx.Err()
}

func (p *placement) moveIntoPlace() error {
	system := on(p.target)
	if _, err := system.lstat(p.target); err == nil && p.exclusive {
		return &TargetExistsError{Path: p.target}
	} else if err == nil {
		p.backup = p.stage + ".old"
		if err := system.rename(p.target, p.backup); err != nil {
			p.backup = ""
			return fmt.Errorf("replace skill folder: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect target folder: %w", err)
	}
	if err := system.rename(p.stage, p.target); err != nil {
		if p.backup != "" {
			_ = system.rename(p.backup, p.target)
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
		restoreErr = errors.Join(restoreErr, on(c.trashed[i].trash).rename(c.trashed[i].trash, c.trashed[i].target))
	}
	for i := len(c.placements) - 1; i >= 0; i-- {
		item := c.placements[i]
		if item.moved {
			if err := removeTree(item.target); err != nil {
				restoreErr = errors.Join(restoreErr, err)
			} else if item.backup != "" {
				restoreErr = errors.Join(restoreErr, on(item.backup).rename(item.backup, item.target))
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
	return on(path).removeTree(path)
}

func randomName(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}
