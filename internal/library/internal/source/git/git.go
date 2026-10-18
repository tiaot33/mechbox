package git

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/gitcmd"
)

const Type = "git"

type locator struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	GitLab     bool   `json:"gitlab,omitempty"`
}

type Kind struct{}

func decode(loc source.Locator) (locator, error) {
	var decoded locator
	if err := json.Unmarshal(loc, &decoded); err != nil {
		return locator{}, fmt.Errorf("decode git locator: %w", err)
	}
	return decoded, nil
}

func (Kind) Describe(loc source.Locator) (source.Description, error) {
	decoded, err := decode(loc)
	if err != nil {
		return source.Description{}, err
	}
	host, repoPath := split(decoded.Repository)
	description := source.Description{Label: decoded.Repository, Ref: decoded.Branch}
	if host != "" {
		description.Label = host + "/" + repoPath
	}
	if decoded.GitLab && host != "" {
		description.ViewURL = "https://" + host + "/" + repoPath + "/-/tree/" + escapeRef(decoded.Branch)
	}
	return description, nil
}

func split(repository string) (host, repoPath string) {
	if rest, ok := strings.CutPrefix(repository, "git@"); ok && !strings.Contains(repository, "://") {
		host, repoPath, _ = strings.Cut(rest, ":")
	} else if u, err := url.Parse(repository); err == nil {
		host, repoPath = u.Host, u.Path
		if u.Scheme == "ssh" {
			host = u.Hostname()
		}
	}
	repoPath = strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git")
	return host, repoPath
}

func escapeRef(ref string) string {
	segments := strings.Split(ref, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func (l locator) remotes() []string {
	if !l.GitLab {
		return []string{l.Repository}
	}
	u, err := url.Parse(l.Repository)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return []string{l.Repository}
	}
	return []string{l.Repository, "git@" + u.Hostname() + ":" + strings.TrimPrefix(u.Path, "/")}
}

func (Kind) Open(ctx context.Context, loc source.Locator, subpath string, _ source.State) (source.Snapshot, error) {
	decoded, err := decode(loc)
	if err != nil {
		return nil, err
	}
	head, err := gitcmd.Head(ctx, decoded.remotes(), decoded.Branch)
	if err != nil {
		return nil, err
	}
	return &snapshot{locator: decoded, subpath: subpath, head: head}, nil
}

type snapshot struct {
	locator     locator
	subpath     string
	head        string
	cloned      bool
	cloneErr    error
	skills      []gitcmd.Skill
	diagnostics []discovery.Diagnostic
}

type state struct {
	Commit string `json:"commit,omitempty"`
	Tree   string `json:"tree,omitempty"`
}

func decodeState(raw source.State) state {
	var decoded state
	if len(raw) > 0 && json.Unmarshal(raw, &decoded) != nil {
		return state{}
	}
	return decoded
}

func encodeState(commit, tree string) source.State {
	encoded, _ := json.Marshal(state{Commit: commit, Tree: tree})
	return encoded
}

func (s *snapshot) Revision() string {
	return s.head
}

func (s *snapshot) clone(ctx context.Context) error {
	if s.cloned {
		return s.cloneErr
	}
	checkout, err := gitcmd.Clone(ctx, s.locator.remotes(), s.locator.Branch, s.subpath)
	if err != nil {
		if ctx.Err() == nil {
			s.cloned, s.cloneErr = true, err
		}
		return err
	}
	s.cloned, s.head, s.skills, s.diagnostics = true, checkout.Commit, checkout.Skills, checkout.Diagnostics
	return nil
}

func (s *snapshot) Candidates(ctx context.Context) ([]discovery.Candidate, []discovery.Diagnostic, error) {
	if err := s.clone(ctx); err != nil {
		return nil, nil, err
	}
	if len(s.skills) == 0 {
		return nil, nil, discovery.NoCandidates(s.diagnostics)
	}
	candidates := make([]discovery.Candidate, len(s.skills))
	for i, skill := range s.skills {
		candidates[i] = skill.Candidate
	}
	return candidates, s.diagnostics, nil
}

func (s *snapshot) Names(ctx context.Context) ([]string, error) {
	if err := s.clone(ctx); err != nil {
		return nil, err
	}
	skills := s.skills
	if s.subpath != "" && s.subpath != "." {
		whole, err := gitcmd.Clone(ctx, s.locator.remotes(), s.head, "")
		if err != nil {
			return nil, err
		}
		skills = whole.Skills
	}
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return names, nil
}

func (s *snapshot) find(name string) (gitcmd.Skill, error) {
	for _, skill := range s.skills {
		if skill.Name == name {
			return skill, nil
		}
	}
	return gitcmd.Skill{}, &source.NotFoundError{What: source.MissingSkill, Name: name}
}

func (s *snapshot) Fetch(ctx context.Context, names []string) ([]source.Skill, error) {
	if err := s.clone(ctx); err != nil {
		return nil, err
	}
	result := make([]source.Skill, 0, len(names))
	for _, name := range names {
		skill, err := s.find(name)
		if err != nil {
			return nil, err
		}
		result = append(result, source.Skill{Name: skill.Name, Description: skill.Description, Files: skill.Files, State: encodeState(s.head, skill.Tree)})
	}
	return result, nil
}

func (s *snapshot) Check(ctx context.Context, have source.Have) (source.Status, source.State, error) {
	stored := decodeState(have.State)
	if stored.Commit != "" && stored.Commit == s.head {
		return source.Unchanged, have.State, nil
	}
	if err := s.clone(ctx); err != nil {
		return source.Unknown, nil, err
	}
	skill, err := s.find(have.Name)
	if err != nil {
		return source.Unknown, nil, err
	}
	switch {
	case stored.Tree == "":
		return source.Unknown, nil, nil
	case stored.Tree == skill.Tree:
		return source.Unchanged, encodeState(s.head, skill.Tree), nil
	default:
		return source.Changed, nil, nil
	}
}

func (s *snapshot) Commits(ctx context.Context, since string) ([]source.Commit, error) {
	since = strings.ToLower(since)
	if !gitcmd.FullCommit.MatchString(since) || since == s.head {
		return nil, nil
	}
	return gitcmd.Commits(ctx, s.locator.remotes(), since, s.head)
}
