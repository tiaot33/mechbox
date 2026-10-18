package gitcmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
)

type Skill struct {
	discovery.Skill
	Tree string
}

type Checkout struct {
	Commit      string
	Skills      []Skill
	Diagnostics []discovery.Diagnostic
}

func Clone(ctx context.Context, remotes []string, ref, subpath string) (result Checkout, err error) {
	if FullCommit.MatchString(ref) {
		ref = strings.ToLower(ref)
	}
	root, err := os.MkdirTemp("", "mechbox-git-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(root)
	repository := filepath.Join(root, "repository")
	if err := ValidateRef(ctx, ref); err != nil {
		return result, err
	}
	clone := func(remote string) error {
		_, err := Run(ctx, "clone", "--quiet", "--depth", "1", "--single-branch", "--branch", ref, "--", remote, repository)
		if err != nil && ctx.Err() == nil && FullCommit.MatchString(ref) {
			if removeErr := os.RemoveAll(repository); removeErr != nil {
				return removeErr
			}
			if _, initErr := Run(ctx, "init", "--quiet", repository); initErr != nil {
				return initErr
			}
			if _, fetchErr := Run(ctx, "-C", repository, "fetch", "--quiet", "--depth", "1", "--", remote, ref); fetchErr != nil {
				return fetchErr
			}
			_, err = Run(ctx, "-C", repository, "checkout", "--quiet", "--detach", "FETCH_HEAD")
		}
		return MissingRef(err, ref)
	}
	var first error
	for i, remote := range remotes {
		if i > 0 {
			if err := os.RemoveAll(repository); err != nil {
				return result, err
			}
		}
		err = clone(remote)
		if err == nil || ctx.Err() != nil {
			break
		}
		if i == 0 {
			first = err
		} else {
			err = Prefer(first, err)
		}
	}
	if err != nil {
		return result, err
	}
	output, err := Run(ctx, "-C", repository, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	fsys := os.DirFS(repository)
	current := "."
	if subpath != "" && subpath != "." {
		for component := range strings.SplitSeq(subpath, "/") {
			entries, err := fs.ReadDir(fsys, current)
			if err != nil {
				return result, err
			}
			index := slices.IndexFunc(entries, func(entry fs.DirEntry) bool { return entry.Name() == component })
			if index < 0 {
				return result, &source.NotFoundError{What: source.MissingSubpath}
			}
			if !entries[index].IsDir() {
				return result, &source.InputError{Reason: "source subpath must contain only real directories"}
			}
			current = path.Join(current, component)
		}
	}
	found, diagnostics, err := discovery.ScanFS(ctx, fsys, current, nil)
	result.Commit = strings.TrimSpace(string(output))
	if err != nil {
		return result, err
	}
	result.Diagnostics = diagnostics
	for _, skill := range found {
		tree, err := treeSHA(ctx, repository, skill.Path)
		if err != nil {
			return Checkout{}, err
		}
		result.Skills = append(result.Skills, Skill{Skill: skill, Tree: tree})
	}
	return result, nil
}

func treeSHA(ctx context.Context, repository, skillPath string) (string, error) {
	spec := "HEAD^{tree}"
	if skillPath != "" && skillPath != "." {
		spec = "HEAD:" + skillPath
	}
	output, err := Run(ctx, "-C", repository, "rev-parse", spec)
	if err != nil {
		return "", err
	}
	sha := strings.ToLower(strings.TrimSpace(string(output)))
	if !FullCommit.MatchString(sha) {
		return "", errors.New("read git tree: invalid skill directory tree SHA")
	}
	return sha, nil
}

func Commits(ctx context.Context, remotes []string, since, head string) ([]source.Commit, error) {
	root, err := os.MkdirTemp("", "mechbox-git-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	repository := filepath.Join(root, "repository")
	if _, err := Run(ctx, "init", "--quiet", "--bare", repository); err != nil {
		return nil, err
	}
	var first error
	for i, remote := range remotes {
		_, err = Run(ctx, "-C", repository, "fetch", "--quiet", "--filter=tree:0", "--", remote, head)
		if err == nil || ctx.Err() != nil {
			break
		}
		if i == 0 {
			first = err
		} else {
			err = Prefer(first, err)
		}
	}
	if err != nil {
		return nil, err
	}
	output, err := Run(ctx, "-C", repository, "log", "--format=%H%x00%an%x00%aI%x00%B%x1e", since+"..FETCH_HEAD")
	if err != nil {
		return nil, err
	}
	var commits []source.Commit
	for record := range strings.SplitSeq(string(output), "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(record, "\n"), "\x00", 4)
		if len(fields) != 4 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, fields[2])
		commits = append(commits, source.Commit{SHA: fields[0], Author: fields[1], Time: at, Message: strings.TrimRight(fields[3], "\n")})
	}
	return commits, nil
}
