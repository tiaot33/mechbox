package agentdir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type Folder struct {
	Name    string
	Path    string
	Target  string
	SkillMD []byte
}

const maxSkillMD = 1 << 20

func List(directory string) ([]Folder, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list skill folders: %w", err)
	}
	var folders []Folder
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		manifest, err := os.Stat(filepath.Join(path, "SKILL.md"))
		if err != nil || !manifest.Mode().IsRegular() {
			continue
		}
		folder := Folder{Name: entry.Name(), Path: path}
		if entry.Type()&fs.ModeSymlink != 0 {
			if folder.Target, err = filepath.EvalSymlinks(path); err != nil {
				continue
			}
		}
		if manifest.Size() <= maxSkillMD {
			folder.SkillMD, _ = os.ReadFile(filepath.Join(path, "SKILL.md"))
		}
		folders = append(folders, folder)
	}
	return folders, nil
}

func Committed(ctx context.Context, root, directory string) (map[string]bool, error) {
	command := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=false", "-C", root, "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", directory)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
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
	return committed, nil
}
