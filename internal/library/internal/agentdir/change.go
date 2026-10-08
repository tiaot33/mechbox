package agentdir

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

type Change struct {
	placements []*placement
	removals   []string
	trashed    []trashed
	done       bool
}

type placement struct {
	target string
	stage  string
	backup string
	moved  bool
}

type trashed struct {
	trash, target string
}

func (c *Change) Stage(ctx context.Context, directory, name string, files []skillfile.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create scope: %w", err)
	}
	stage, err := os.MkdirTemp(directory, "."+name+"-")
	if err != nil {
		return fmt.Errorf("stage skill folder: %w", err)
	}
	c.placements = append(c.placements, &placement{target: filepath.Join(directory, name), stage: stage})
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
	if _, err := os.Lstat(p.target); err == nil {
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
			if err := os.RemoveAll(item.target); err != nil {
				restoreErr = errors.Join(restoreErr, err)
			} else if item.backup != "" {
				restoreErr = errors.Join(restoreErr, os.Rename(item.backup, item.target))
			}
		}
		if item.stage != "" {
			restoreErr = errors.Join(restoreErr, os.RemoveAll(item.stage))
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
			_ = os.RemoveAll(item.backup)
		}
	}
	for _, entry := range c.trashed {
		_ = os.RemoveAll(entry.trash)
	}
}

func randomName(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}
