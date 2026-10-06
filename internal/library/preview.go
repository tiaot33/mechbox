package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
)

const previewTTL = 30 * time.Minute

type previewState struct {
	source      Source
	snapshot    source.Snapshot
	candidates  []discovery.Candidate
	diagnostics []discovery.Diagnostic
	timer       *time.Timer
	fetching    bool
	audits      map[string]SkillAudit
}

func (l *sqliteLibrary) PreviewFetch(ctx context.Context, req PreviewFetchRequest) (preview FetchPreview, err error) {
	defer func() { err = typedError(err) }()
	l.previewMu.Lock()
	closed := l.closed
	l.previewMu.Unlock()
	if closed {
		return FetchPreview{}, fmt.Errorf("preview fetch: library is closed")
	}
	source, subpath, names, err := l.previewSource(ctx, req)
	if err != nil {
		return FetchPreview{}, err
	}
	var candidates []discovery.Candidate
	var diagnostics []discovery.Diagnostic
	snapshot, err := l.openSnapshot(ctx, source, subpath, nil)
	if err == nil {
		candidates, diagnostics, err = snapshot.Candidates(ctx)
	}
	if err != nil {
		err = typedError(err)
		if typed, ok := errors.AsType[*Error](err); ok && source.Type == SourceLocal && typed.Details.Field == "path" {
			typed.Details.Field = "local"
		}
		return FetchPreview{}, err
	}
	candidates, err = selectCandidates(candidates, names)
	if err != nil {
		return FetchPreview{}, err
	}
	source.LastFetched = time.Now().UTC()
	source.LastSeenRef = snapshot.Revision()
	id, err := newPreviewID()
	if err != nil {
		return FetchPreview{}, err
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	if l.closed {
		return FetchPreview{}, fmt.Errorf("preview fetch: library is closed")
	}
	if err := ctx.Err(); err != nil {
		return FetchPreview{}, err
	}
	state := &previewState{source: source, snapshot: snapshot, candidates: candidates, diagnostics: diagnostics}
	listed, err := l.listCandidates(ctx, state)
	if err != nil {
		return FetchPreview{}, err
	}
	l.previews[id] = state
	state.timer = time.AfterFunc(previewTTL, func() {
		l.previewMu.Lock()
		defer l.previewMu.Unlock()
		if current := l.previews[id]; current == state && !current.fetching {
			delete(l.previews, id)
		}
	})
	preview = FetchPreview{ID: id, Revision: source.LastSeenRef, Candidates: listed, Diagnostics: previewDiagnostics(state.diagnostics)}
	if source.Type == SourceGitHub {
		preview.Ref = source.Locator.Branch
	}
	return preview, nil
}

func (l *sqliteLibrary) previewSource(ctx context.Context, req PreviewFetchRequest) (source Source, subpath string, names []string, err error) {
	var given []string
	if req.GitHub != nil {
		given = append(given, "github")
	}
	if req.URL != "" {
		given = append(given, "url")
	}
	if req.Local != "" {
		given = append(given, "local")
	}
	switch len(given) {
	case 0:
		return Source{}, "", nil, argumentError("preview fetch", "", "one of github, url or local is required")
	case 1:
	default:
		return Source{}, "", nil, argumentError("preview fetch", given[1], "only one of github, url or local may be given")
	}
	switch {
	case req.GitHub != nil:
		resolved, err := github.Resolve(ctx, github.Input{Input: req.GitHub.Input, Ref: req.GitHub.Ref, Subpath: req.GitHub.Subpath})
		if err != nil {
			return Source{}, "", nil, err
		}
		if resolved.Name != "" {
			names = []string{resolved.Name}
		}
		return Source{Type: SourceGitHub, Locator: SourceLocator{Repository: resolved.Repository, Branch: resolved.Ref}}, resolved.Subpath, names, nil
	case req.URL != "":
		return Source{Type: SourceURL, Locator: SourceLocator{URL: req.URL}}, "", nil, nil
	default:
		root, err := filepath.Abs(req.Local)
		if err != nil {
			return Source{}, "", nil, fmt.Errorf("resolve source path: %w", err)
		}
		return Source{Type: SourceLocal, Locator: SourceLocator{Path: root}}, "", nil, nil
	}
}

func (l *sqliteLibrary) listCandidates(ctx context.Context, state *previewState) ([]Candidate, error) {
	locator, err := json.Marshal(state.source.Locator)
	if err != nil {
		return nil, fmt.Errorf("encode source locator: %w", err)
	}
	fetched, err := l.store.SkillsFromSource(ctx, string(state.source.Type), locator)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, len(state.candidates))
	for i, c := range state.candidates {
		candidates[i] = Candidate{Fetched: SkillID(fetched[c.Name]), PluginName: c.PluginName, Name: c.Name, Description: c.Description, Path: c.Path}
	}
	return candidates, nil
}

func previewDiagnostics(diagnostics []discovery.Diagnostic) []PreviewDiagnostic {
	if diagnostics == nil {
		return nil
	}
	result := make([]PreviewDiagnostic, len(diagnostics))
	for i, d := range diagnostics {
		result[i] = PreviewDiagnostic{Path: d.Path, Reason: d.Reason}
	}
	return result
}

func (l *sqliteLibrary) Fetch(ctx context.Context, id FetchPreviewID, names []string) (fetched []SkillSummary, err error) {
	defer func() { err = typedError(err) }()
	if len(names) == 0 {
		return nil, argumentError("fetch", "names", "at least one skill name is required")
	}
	state, err := l.claimPreview(id, names)
	if err != nil {
		return nil, err
	}
	defer func() { l.releasePreview(id, state, err == nil) }()
	skills, err := state.snapshot.Fetch(ctx, names)
	if err != nil {
		return nil, err
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	created, err := l.fetchAll(ctx, state.source, skills)
	if err != nil {
		return nil, err
	}
	fetched = make([]SkillSummary, len(created))
	for i, skill := range created {
		fetched[i] = SkillSummary{ID: skill.ID, Name: skill.Name, Description: skill.Description, Source: skill.Source, FetchedAt: skill.FetchedAt, UpdatedAt: skill.UpdatedAt}
	}
	return fetched, nil
}

func (l *sqliteLibrary) claimPreview(id FetchPreviewID, names []string) (*previewState, error) {
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	state := l.previews[id]
	if state == nil {
		return nil, &Error{Code: NotFound, Message: "fetch: unknown or discarded preview"}
	}
	if state.fetching {
		return nil, preconditionError("fetch", "another fetch from this preview is in progress", ErrorDetails{})
	}
	if _, err := selectCandidates(state.candidates, names); err != nil {
		return nil, err
	}
	state.fetching = true
	if state.timer != nil {
		state.timer.Stop()
	}
	return state, nil
}

func (l *sqliteLibrary) releasePreview(id FetchPreviewID, state *previewState, fetched bool) {
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	if l.previews[id] != state {
		return
	}
	if fetched {
		delete(l.previews, id)
		return
	}
	state.fetching = false
	if state.timer != nil {
		state.timer.Reset(previewTTL)
	}
}

func newPreviewID() (FetchPreviewID, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("issue preview handle: %w", err)
	}
	return FetchPreviewID(hex.EncodeToString(buffer)), nil
}

func selectCandidates(candidates []discovery.Candidate, selected []string) ([]discovery.Candidate, error) {
	return selectNamed(candidates, selected, func(candidate discovery.Candidate) string { return candidate.Name })
}

func selectNamed[T any](items []T, selected []string, nameOf func(T) string) ([]T, error) {
	if len(selected) == 0 {
		return items, nil
	}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		wanted[name] = true
	}
	result := make([]T, 0, len(selected))
	for _, item := range items {
		if name := nameOf(item); wanted[name] {
			result = append(result, item)
			delete(wanted, name)
		}
	}
	if len(wanted) != 0 {
		for name := range wanted {
			return nil, &Error{Code: NotFound, Message: fmt.Sprintf("source skill %q not found", name), Details: ErrorDetails{Name: name}}
		}
	}
	return result, nil
}
