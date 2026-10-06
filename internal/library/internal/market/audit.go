package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const auditTimeout = 5 * time.Second

type AuditEntry struct {
	Provider   string
	Slug       string
	Status     string
	Summary    string
	AuditedAt  time.Time
	RiskLevel  string
	Categories []string
	DetailURL  string
}

func Audit(ctx context.Context, owner, repo, name string) ([]AuditEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	id := url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/" + url.PathEscape(name)
	response, err := get(ctx, baseURL+"/api/v1/skills/audit/"+id)
	if err != nil {
		return nil, fmt.Errorf("audit skill: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("audit skill: HTTP %d: %w", response.StatusCode, ErrUnreachable)
	}
	body, err := readBody(ctx, response)
	if err != nil {
		return nil, fmt.Errorf("read audit response: %w", err)
	}
	var payload struct {
		Audits []struct {
			Provider   string   `json:"provider"`
			Slug       string   `json:"slug"`
			Status     string   `json:"status"`
			Summary    string   `json:"summary"`
			AuditedAt  string   `json:"auditedAt"`
			RiskLevel  string   `json:"riskLevel"`
			Categories []string `json:"categories"`
		} `json:"audits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Audits == nil {
		return nil, fmt.Errorf("read audit response: invalid response format: %w", ErrInvalidResponse)
	}
	if len(payload.Audits) == 0 {
		return nil, nil
	}
	entries := make([]AuditEntry, 0, len(payload.Audits))
	for _, audit := range payload.Audits {
		if strings.TrimSpace(audit.Provider) == "" || strings.TrimSpace(audit.Slug) == "" || strings.TrimSpace(audit.Status) == "" || strings.TrimSpace(audit.Summary) == "" {
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
			DetailURL:  baseURL + "/" + id + "/security/" + url.PathEscape(audit.Slug),
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("read audit response: no valid audit entries: %w", ErrInvalidResponse)
	}
	return entries, nil
}
