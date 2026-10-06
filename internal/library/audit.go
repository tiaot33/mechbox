package library

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

const (
	auditRequestTimeout = 5 * time.Second
	maxAuditSize        = 1 << 20
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
	owner, repo, ok := githubOwnerRepo(state.source.Locator.Repository)
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
	unavailable := SkillAudit{Availability: AuditUnavailable}
	ctx, cancel := context.WithTimeout(ctx, auditRequestTimeout)
	defer cancel()
	id := auditPath(owner, repo, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://skills.sh/api/v1/skills/audit/"+id, nil)
	if err != nil {
		return unavailable
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return unavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return SkillAudit{Availability: AuditNone}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return unavailable
	}
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, maxAuditSize+1))
	if err != nil || len(body) > maxAuditSize {
		return unavailable
	}
	var payload struct {
		Audits []struct {
			Provider   string      `json:"provider"`
			Slug       string      `json:"slug"`
			Status     AuditStatus `json:"status"`
			Summary    string      `json:"summary"`
			AuditedAt  string      `json:"auditedAt"`
			RiskLevel  string      `json:"riskLevel"`
			Categories []string    `json:"categories"`
		} `json:"audits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Audits == nil {
		return unavailable
	}
	if len(payload.Audits) == 0 {
		return SkillAudit{Availability: AuditNone}
	}
	entries := make([]AuditEntry, 0, len(payload.Audits))
	for _, audit := range payload.Audits {
		if strings.TrimSpace(audit.Provider) == "" || strings.TrimSpace(audit.Slug) == "" || strings.TrimSpace(string(audit.Status)) == "" || strings.TrimSpace(audit.Summary) == "" {
			continue
		}
		auditedAt, _ := time.Parse(time.RFC3339Nano, audit.AuditedAt)
		entries = append(entries, AuditEntry{
			Provider:   audit.Provider,
			Slug:       audit.Slug,
			Status:     audit.Status,
			Summary:    audit.Summary,
			AuditedAt:  auditedAt.UTC(),
			RiskLevel:  audit.RiskLevel,
			Categories: audit.Categories,
			DetailURL:  "https://skills.sh/" + id + "/security/" + url.PathEscape(audit.Slug),
		})
	}
	if len(entries) == 0 {
		return unavailable
	}
	return SkillAudit{Availability: AuditAvailable, Entries: entries}
}

func auditPath(owner, repo, name string) string {
	return url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/" + url.PathEscape(name)
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
