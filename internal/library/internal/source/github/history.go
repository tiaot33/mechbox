package github

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/gitcmd"
)

const comparePageSize = 100

type githubCompareResponse struct {
	TotalCommits int `json:"total_commits"`
	Commits      []struct {
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
	if !gitcmd.FullCommit.MatchString(since) || since == s.ref {
		return nil, nil
	}
	if s.client == nil {
		return gitcmd.Commits(ctx, remotes(s.locator.Repository), since, s.ref)
	}
	var commits []source.Commit
	for page := 1; ; page++ {
		var payload githubCompareResponse
		location := githubAPIURL("/repos/%s/%s/compare/%s", s.owner, s.repo, since+"..."+s.ref) + fmt.Sprintf("?per_page=%d&page=%d", comparePageSize, page)
		if err := s.client.get(ctx, location, &payload); err != nil {
			return nil, err
		}
		for _, commit := range payload.Commits {
			commits = append(commits, source.Commit{SHA: commit.SHA, Message: commit.Commit.Message, Author: commit.Commit.Author.Name, Time: commit.Commit.Author.Date})
		}
		if len(payload.Commits) < comparePageSize || len(commits) >= payload.TotalCommits {
			break
		}
	}
	slices.Reverse(commits)
	return commits, nil
}
