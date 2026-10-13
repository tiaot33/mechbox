package github

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

type githubCompareResponse struct {
	Commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string    `json:"name"`
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	} `json:"commits"`
}

func (s *githubSnapshot) Names(ctx context.Context) ([]string, error) {
	whole := s
	if s.subpath != "" && s.subpath != "." {
		whole = &githubSnapshot{ref: s.ref, rootTree: s.rootTree, entries: s.entries, byPath: s.byPath, owner: s.owner, repo: s.repo, locator: s.locator, client: s.client, storedBlobs: s.storedBlobs}
		if s.client == nil {
			if err := whole.clone(ctx, s.ref); err != nil {
				return nil, err
			}
		}
	}
	if err := whole.discover(ctx); err != nil {
		return nil, err
	}
	names := make([]string, len(whole.skills))
	for i, skill := range whole.skills {
		names[i] = skill.name
	}
	return names, nil
}

func (s *githubSnapshot) Commits(ctx context.Context, since string) ([]source.Commit, error) {
	since = strings.ToLower(since)
	if !fullCommit.MatchString(since) || since == s.ref {
		return nil, nil
	}
	if s.client == nil {
		return gitCommits(ctx, s.locator, since, s.ref)
	}
	var payload githubCompareResponse
	if err := s.client.get(ctx, githubAPIURL("/repos/%s/%s/compare/%s", s.owner, s.repo, since+"..."+s.ref), &payload); err != nil {
		return nil, err
	}
	commits := make([]source.Commit, len(payload.Commits))
	for i, commit := range payload.Commits {
		commits[i] = source.Commit{SHA: commit.SHA, Message: commit.Commit.Message, Author: commit.Commit.Author.Name, Time: commit.Commit.Author.Date}
	}
	slices.Reverse(commits)
	return commits, nil
}

func gitCommits(ctx context.Context, locator repoLocator, since, head string) ([]source.Commit, error) {
	root, err := os.MkdirTemp("", "skills-git-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	repository := filepath.Join(root, "repository")
	if _, err := runSourceGit(ctx, "init", "--quiet", "--bare", repository); err != nil {
		return nil, err
	}
	fetch := func(remote string) error {
		_, err := runSourceGit(ctx, "-C", repository, "fetch", "--quiet", "--filter=tree:0", "--", remote, head)
		return err
	}
	err = fetch(locator.Repository)
	if err != nil && ctx.Err() == nil {
		if remote, ok := strings.CutPrefix(locator.Repository, "https://github.com/"); ok {
			err = fetch("git@github.com:" + remote)
		}
	}
	if err != nil {
		return nil, err
	}
	output, err := runSourceGit(ctx, "-C", repository, "log", "--format=%H%x00%an%x00%aI%x00%B%x1e", since+"..FETCH_HEAD")
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
