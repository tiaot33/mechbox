package gitcmd

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
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
		_, err := Run(ctx, "clone", "--quiet", "--depth", "1", "--single-branch", "--filter=blob:none", "--no-checkout", "--origin", "origin", "--branch", ref, "--", remote, repository)
		if err != nil && ctx.Err() == nil && FullCommit.MatchString(ref) {
			if removeErr := os.RemoveAll(repository); removeErr != nil {
				return removeErr
			}
			if _, initErr := Run(ctx, "init", "--quiet", repository); initErr != nil {
				return initErr
			}
			if _, remoteErr := Run(ctx, "-C", repository, "remote", "add", "--", "origin", remote); remoteErr != nil {
				return remoteErr
			}
			if _, fetchErr := Run(ctx, "-C", repository, "fetch", "--quiet", "--no-tags", "--depth", "1", "--filter=blob:none", "origin", ref); fetchErr != nil {
				return fetchErr
			}
			_, err = Run(ctx, "-C", repository, "update-ref", "--no-deref", "HEAD", "FETCH_HEAD")
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
	view, err := readTree(ctx, repository)
	if err != nil {
		return result, err
	}
	current := "."
	if subpath != "" && subpath != "." {
		for component := range strings.SplitSeq(subpath, "/") {
			entries, err := view.ReadDir(current)
			if err != nil {
				return result, err
			}
			index := slices.IndexFunc(entries, func(entry discovery.Entry) bool { return entry.Name == component })
			if index < 0 {
				return result, &source.NotFoundError{What: source.MissingSubpath}
			}
			if !entries[index].Mode.IsDir() {
				return result, &source.InputError{Reason: "source subpath must contain only real directories"}
			}
			current = path.Join(current, component)
		}
	}
	listed := map[string][]treeFile{}
	candidates, diagnostics, err := discovery.Scan(ctx, view, current, func(candidate discovery.Candidate, _ string, _ []byte) error {
		files, err := view.skillFiles(candidate.Path)
		listed[candidate.Path] = files
		return err
	})
	result.Commit = strings.TrimSpace(string(output))
	if err != nil {
		return result, err
	}
	var oids []string
	for _, candidate := range candidates {
		for _, file := range listed[candidate.Path] {
			oids = append(oids, file.oid)
		}
	}
	blobs, err := view.blobs(ctx, oids)
	if err != nil {
		return result, err
	}
	result.Diagnostics = diagnostics
	for _, candidate := range candidates {
		tree, err := view.treeSHA(candidate.Path)
		if err != nil {
			return Checkout{}, err
		}
		files := make([]skillfile.File, len(listed[candidate.Path]))
		for i, file := range listed[candidate.Path] {
			files[i] = skillfile.File{Path: file.path, Content: bytes.Clone(blobs[file.oid]), Mode: file.mode}
			if files[i].Content == nil {
				files[i].Content = []byte{}
			}
		}
		result.Skills = append(result.Skills, Skill{Skill: discovery.Skill{Candidate: candidate, Files: files}, Tree: tree})
	}
	return result, nil
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
