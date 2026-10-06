package library

import (
	"context"
	"slices"

	"github.com/tiaot33/skills-tui/internal/library/internal/market"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
)

func (l *sqliteLibrary) GetSkillAudit(ctx context.Context, id FetchPreviewID, name string) (audit *SkillAudit, err error) {
	defer func() { err = typedError(err) }()
	if !discovery.ValidName(name) {
		return nil, &Error{Code: InvalidArgument, Message: "invalid candidate name"}
	}
	l.previewMu.Lock()
	state := l.previews[id]
	var cached SkillAudit
	var hit bool
	if state != nil {
		cached, hit = state.audits[name]
	}
	l.previewMu.Unlock()
	if state == nil {
		return nil, &Error{Code: NotFound, Message: "get skill audit: unknown or discarded preview"}
	}
	if _, err := selectCandidates(state.candidates, []string{name}); err != nil {
		return nil, err
	}
	owner, repo, ok := github.OwnerRepo(state.source.Locator.Repository)
	if !ok {
		return nil, nil
	}
	if hit {
		return cloneAudit(&cached), nil
	}
	got := lookupAudit(ctx, owner, repo, name)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if got.Availability != AuditUnavailable {
		l.previewMu.Lock()
		if state.audits == nil {
			state.audits = map[string]SkillAudit{}
		}
		state.audits[name] = got
		l.previewMu.Unlock()
	}
	return cloneAudit(&got), nil
}

func lookupAudit(ctx context.Context, owner, repo, name string) SkillAudit {
	found, err := market.Audit(ctx, owner, repo, name)
	if err != nil {
		return SkillAudit{Availability: AuditUnavailable}
	}
	if len(found) == 0 {
		return SkillAudit{Availability: AuditNone}
	}
	entries := make([]AuditEntry, len(found))
	for i, entry := range found {
		entries[i] = AuditEntry{
			Provider:   entry.Provider,
			Slug:       entry.Slug,
			Status:     AuditStatus(entry.Status),
			Summary:    entry.Summary,
			AuditedAt:  entry.AuditedAt,
			RiskLevel:  entry.RiskLevel,
			Categories: entry.Categories,
			DetailURL:  entry.DetailURL,
		}
	}
	return SkillAudit{Availability: AuditAvailable, Entries: entries}
}

func cloneAudit(audit *SkillAudit) *SkillAudit {
	if audit == nil {
		return nil
	}
	cloned := *audit
	cloned.Entries = slices.Clone(audit.Entries)
	for i := range cloned.Entries {
		cloned.Entries[i].Categories = slices.Clone(cloned.Entries[i].Categories)
	}
	return &cloned
}
