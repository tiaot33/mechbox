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
)

const (
	auditRequestTimeout = 5 * time.Second // 单个候选的请求超时
	maxAuditSize        = 1 << 20         // 单个审计响应的字节上限
)

// FetchSkillAudit 在用户查看单个候选时才查 skills.sh。审计只是展示用的覆盖层:任何失败都只降级为
// Availability 状态;联网期间不持任何锁,已确定的结果写回会话,不可用的结果留待下次重查。
func (l *sqliteLibrary) FetchSkillAudit(ctx context.Context, id FetchID, name string) (audit *SkillAudit, err error) {
	defer func() { err = typedError(err) }()
	if !validSelectionName(name) {
		return nil, &Error{Code: InvalidArgument, Message: "invalid Fetch name selection"}
	}
	l.fetchMu.Lock()
	session := l.fetches[id]
	var cached SkillAudit
	var hit bool
	if session != nil {
		cached, hit = session.audits[name]
	}
	l.fetchMu.Unlock()
	if session == nil {
		return nil, &Error{Code: NotFound, Message: "fetch skill audit: unknown or discarded fetch"}
	}
	// 候选与来源在会话建立后只读,可在锁外读取。
	if _, err := selectSkills(session.skills, []string{name}); err != nil {
		return nil, err
	}
	owner, repo, ok := githubOwnerRepo(session.source.Locator.Repository)
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
		l.fetchMu.Lock()
		if session.audits == nil {
			session.audits = map[string]SkillAudit{}
		}
		session.audits[name] = got
		l.fetchMu.Unlock()
	}
	return cloneAudit(&got), nil
}

// lookupAudit 请求 https://skills.sh/api/v1/skills/audit/{owner}/{repo}/{name}。路径逐段转义、
// 不编码斜杠;不带 Authorization(匿名可读,带无效凭据反而 401)。404 表示尚无审计,其余失败为不可用。
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
	// 解析宽松:必填字段缺失只丢该条;auditedAt 解析失败保留条目、时间为零值。
	entries := make([]AuditEntry, 0, len(payload.Audits))
	for _, audit := range payload.Audits {
		if strings.TrimSpace(audit.Provider) == "" || strings.TrimSpace(audit.Slug) == "" || strings.TrimSpace(string(audit.Status)) == "" || strings.TrimSpace(audit.Summary) == "" {
			continue
		}
		auditedAt, _ := time.Parse(time.RFC3339Nano, audit.AuditedAt) // 失败即零值
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

// auditPath 把 skills.sh 的 skill id(owner/repo/name)按路径段转义后拼接;整体 QueryEscape 会被 400 拒绝。
func auditPath(owner, repo, name string) string {
	return url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/" + url.PathEscape(name)
}

// cloneAudit 深拷贝审计快照,使返回给调用方的结果不与会话缓存共享可变数据。
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
