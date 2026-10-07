package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/market"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/download"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// SearchSkill 是 skills.sh 搜索到的一个技能,只供展示;Preview 原样传给 PreviewFetch,入库预览只列这一个技能。
type SearchSkill struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	DetailURL string              `json:"detail_url"`
	Installs  *int64              `json:"installs,omitempty"` // skills.sh 统计的安装量
	Preview   PreviewFetchRequest `json:"preview"`            // 原样传给 PreviewFetch,入库预览只列这一个技能
}

// PreviewFetchRequest 是入库预览的输入:GitHub / URL / Local 恰好给一个。
type PreviewFetchRequest struct {
	GitHub *GitHubInput `json:"github,omitempty"`
	URL    string       `json:"url,omitempty"`   // zip 压缩包地址
	Local  string       `json:"local,omitempty"` // 本地目录;相对路径按当前工作目录解析为绝对路径
}

// GitHubInput 是用户输入的 GitHub 地址,由 PreviewFetch 解析。
type GitHubInput struct {
	Input   string `json:"input"`             // owner/repo、owner/repo@name、HTTPS / SSH 仓库地址、tree URL,可带 #ref;@name 时入库预览只列该技能
	Ref     string `json:"ref,omitempty"`     // 显式分支或 tag;带斜杠的必须从这里传;省略时发现默认分支
	Subpath string `json:"subpath,omitempty"` // 仓库内目录
}

// FetchPreviewID 是一次入库预览的不透明句柄,由 Library 签发。它不持久化、不参与
// Sync、也不跨 Library 实例共享;进程退出、Close 或超时后被后端回收。
type FetchPreviewID string

// AuditAvailability 说明一个候选在 skills.sh 上的审计查询结果处于哪种状态。
type AuditAvailability string

const (
	AuditAvailable   AuditAvailability = "available"   // 至少拿到一条合作方结论
	AuditNone        AuditAvailability = "none"        // skills.sh 没有该 skill 的任何审计(404 或空列表)
	AuditUnavailable AuditAvailability = "unavailable" // 网络故障、超时、非 404 错误或响应无法解析
)

// AuditStatus 是 skills.sh 归一化后的合作方结论;文档三值以常量暴露,未知值原样透传。
type AuditStatus string

const (
	AuditPass AuditStatus = "pass"
	AuditWarn AuditStatus = "warn"
	AuditFail AuditStatus = "fail"
)

// AuditEntry 是 skills.sh 某个合作方对一个 skill 的审计结论;只供展示,不作决策依据。
type AuditEntry struct {
	Provider   string      `json:"provider"`             // 合作方展示名
	Slug       string      `json:"slug"`                 // 合作方 slug
	Status     AuditStatus `json:"status"`               // pass / warn / fail;未知值原样保留
	Summary    string      `json:"summary"`              // 一行摘要
	AuditedAt  time.Time   `json:"audited_at,omitzero"`  // 该次分析时间;解析失败为零值。它不代表对应当前文件树
	RiskLevel  string      `json:"risk_level,omitempty"` // 合作方风险等级,原样字符串(live 出现过文档外的 SAFE)
	Categories []string    `json:"categories,omitempty"` // 风险类别(目前仅 Agent Trust Hub)
	DetailURL  string      `json:"detail_url"`           // skills.sh 上该合作方的安全详情页
}

// SkillAudit 是一个入库预览候选在 skills.sh 上的审计概览:查看候选时经 GetSkillAudit 按需查询,随入库预览缓存,
// 只供展示。不入库、不参与 Sync、不影响入库。Entries 仅在 Availability 为 AuditAvailable 时非空。
type SkillAudit struct {
	Availability AuditAvailability `json:"availability"`
	Entries      []AuditEntry      `json:"entries,omitempty"`
}

// FetchPreview 是一次入库预览的句柄及其候选;此时尚无内容进入 Library。
type FetchPreview struct {
	ID          FetchPreviewID      `json:"id"`
	Ref         string              `json:"ref,omitempty"` // Git 类来源实际使用的分支或 tag,省略时为发现的默认分支;其他来源为空
	Revision    string              `json:"revision"`      // 钉住的内容:GitHub 为 commit,其他为内容哈希
	Candidates  []Candidate         `json:"candidates"`
	Diagnostics []PreviewDiagnostic `json:"diagnostics,omitempty"`
}

// Candidate 是入库预览中可挑选入库的候选;skills.sh 审计不随候选返回,见 GetSkillAudit。
type Candidate struct {
	Fetched     SkillID `json:"fetched,omitzero"`      // 同一来源同名的库内技能 ID;入库预览时查询,零值表示未入库
	PluginName  string  `json:"plugin_name,omitempty"` // 清单显式声明的插件分组名,仅在入库预览中展示
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Path        string  `json:"path"` // 相对来源根目录
}

// PreviewDiagnostic 说明一个路径为何未成为候选。
type PreviewDiagnostic struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// SearchSkills 只读搜索 skills.sh,最多返回 20 项;服务失败与空结果分别返回。
// 无法转换为可做入库预览的 GitHub 地址的条目直接过滤掉。
func (l *Library) SearchSkills(ctx context.Context, query string) (result []SearchSkill, err error) {
	defer func() { err = typedError(err) }()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, &Error{Code: InvalidArgument, Message: "search query must not be empty"}
	}
	skills, err := market.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	result = []SearchSkill{}
	for _, skill := range skills {
		repository, ok := github.PlainRepository(skill.Source)
		if !ok || !discovery.ValidName(skill.Name) || strings.Contains(skill.Name, "#") {
			continue
		}
		preview := PreviewFetchRequest{GitHub: &GitHubInput{Input: repository + "@" + skill.Name}}
		result = append(result, SearchSkill{ID: skill.ID, Name: skill.Name, DetailURL: skill.DetailURL, Installs: skill.Installs, Preview: preview})
	}
	return result, nil
}

const previewTTL = 30 * time.Minute

type previewState struct {
	source      store.Source
	snapshot    source.Snapshot
	candidates  []discovery.Candidate
	diagnostics []discovery.Diagnostic
	timer       *time.Timer
	fetching    bool
	audits      map[string]SkillAudit
}

// PreviewFetch 发起一次入库预览:发现来源中的候选 skill,返回句柄与候选清单,此时尚无内容进入 Library。
// req 的 GitHub / URL / Local 恰好给一个,多给或全空返回 InvalidArgument:多给时 Details.Field 指向多出的字段,全空时为空;
// 所给的输入本身无效时 Details.Field 为该字段(github / url / local)。
// GitHub 输入在这里解析:输入无效、tree URL 的 ref 边界有歧义、#ref 与 Ref 不一致、Subpath 与 tree URL
// 路径不一致返回 InvalidArgument;省略 ref 时发现默认分支,失败返回 SourceUnreachable,不假定 main;
// @name 时入库预览只列该技能,不存在返回 NotFound。URL 与本地目录总是列出全部候选。
// 入库预览钉住来源当时的内容:GitHub 只取各候选 SKILL.md 并钉住当时的 commit;URL 一次下载完整归档;
// 本地目录把候选文件读进内存作为快照(候选文件累计超过 1000 个或 64 MiB 返回 SourceInvalid),
// 之后目录再被改动也不影响入库。本地来源的身份是该目录的绝对路径,同一目录下的技能归入同一 Collection。
// 一个有效候选都没有时返回 SourceInvalid,诊断写在错误信息里。
// 返回的候选元数据与诊断供调用方保存、展示,修改返回值不影响入库预览。
// 句柄在 30 分钟后或 Close 时失效;过期、未知或其他 Library 实例的句柄在入库时返回 NotFound。
func (l *Library) PreviewFetch(ctx context.Context, req PreviewFetchRequest) (preview FetchPreview, err error) {
	defer func() { err = typedError(err) }()
	l.previewMu.Lock()
	closed := l.closed
	l.previewMu.Unlock()
	if closed {
		return FetchPreview{}, fmt.Errorf("preview fetch: library is closed")
	}
	target, field, err := previewTarget(ctx, req)
	if err != nil {
		return FetchPreview{}, inputField(err, field)
	}
	var candidates []discovery.Candidate
	var diagnostics []discovery.Diagnostic
	snapshot, err := l.sources.Open(ctx, target.Type, target.Locator, target.Subpath, nil)
	if err == nil {
		candidates, diagnostics, err = snapshot.Candidates(ctx)
	}
	if err != nil {
		return FetchPreview{}, inputField(err, field)
	}
	candidates, err = selectCandidates(candidates, target.Names)
	if err != nil {
		return FetchPreview{}, err
	}
	record := store.Source{Type: target.Type, Locator: target.Locator}
	description, err := l.sources.Describe(record.Type, record.Locator)
	if err != nil {
		return FetchPreview{}, err
	}
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
	state := &previewState{source: record, snapshot: snapshot, candidates: candidates, diagnostics: diagnostics}
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
	return FetchPreview{ID: id, Ref: description.Ref, Revision: snapshot.Revision(), Candidates: listed, Diagnostics: previewDiagnostics(state.diagnostics)}, nil
}

func previewTarget(ctx context.Context, req PreviewFetchRequest) (target source.Target, field string, err error) {
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
		return source.Target{}, "", argumentError("preview fetch", "", "one of github, url or local is required")
	case 1:
	default:
		return source.Target{}, "", argumentError("preview fetch", given[1], "only one of github, url or local may be given")
	}
	switch {
	case req.GitHub != nil:
		target, err = github.Locate(ctx, github.Input{Input: req.GitHub.Input, Ref: req.GitHub.Ref, Subpath: req.GitHub.Subpath})
	case req.URL != "":
		target, err = download.Locate(req.URL)
	default:
		target, err = local.Locate(req.Local)
	}
	return target, given[0], err
}

func inputField(err error, field string) error {
	if _, ok := errors.AsType[*source.InputError](err); ok {
		return &Error{Code: InvalidArgument, Message: err.Error(), Details: ErrorDetails{Field: field}, Err: err}
	}
	return err
}

func (l *Library) listCandidates(ctx context.Context, state *previewState) ([]Candidate, error) {
	fetched, err := l.store.SkillsFromSource(ctx, state.source.Type, state.source.Locator)
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

// Fetch 从一次入库预览中选取候选原子提交入库,返回入库的技能。
// names 为空返回 InvalidArgument;"全部入库"由调用方把 Fetched 为零的候选名收集起来传入。
// 所选技能中有已入库的,整批返回 AlreadyExists。
// GitHub 来源可在入库预览已钉住的 commit 上联网取所选 Skill 文件,不重新解析 ref,
// 不跟随当前 HEAD。URL / 本地目录只读取入库预览时的内容,不再读取来源。要么所选 skill 全部入库,
// 要么库保持不变。成功后该入库预览句柄被丢弃(含未选候选),再次使用返回 NotFound;
// 失败时保留句柄供重试,直至 Close 或过期。同一句柄的一次入库尚在进行时,再次入库返回
// FailedPrecondition;联网期间不持库锁,其他入库预览 / 列表 / 安装不必等它。
func (l *Library) Fetch(ctx context.Context, id FetchPreviewID, names []string) (fetched []Skill, err error) {
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
	return l.fetchAll(ctx, state.source, state.snapshot.Revision(), skills)
}

func (l *Library) claimPreview(id FetchPreviewID, names []string) (*previewState, error) {
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

func (l *Library) releasePreview(id FetchPreviewID, state *previewState, fetched bool) {
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

// GetSkillAudit 按需向 skills.sh 查询一次入库预览中某个技能的审计,供查看单个技能时懒加载。
// 只供展示:不入库、不参与 Sync、不挡入库;查询失败降级为 AuditUnavailable,不返回错误。
// 结果随入库预览缓存,AuditUnavailable 不缓存、再次调用会重查。URL / 本地目录与非 github.com 来源
// 不在 skills.sh 收录范围,返回 nil 且不联网。未知或已失效句柄、不在候选中的名字返回 NotFound。
func (l *Library) GetSkillAudit(ctx context.Context, id FetchPreviewID, name string) (audit *SkillAudit, err error) {
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
	owner, repo, ok := github.OwnerRepo(state.source.Type, state.source.Locator)
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

func skillDescription(files []skillfile.File) string {
	for _, file := range files {
		if file.Path != "SKILL.md" {
			continue
		}
		meta, err := discovery.ParseSkillMD(file.Content, ".")
		if err != nil {
			return ""
		}
		return meta.Description
	}
	return ""
}

func (l *Library) fetchAll(ctx context.Context, record store.Source, revision string, skills []source.Skill) ([]Skill, error) {
	fetched := make([]Skill, 0, len(skills))
	err := l.store.InTx(ctx, func(tx *store.Tx) error {
		sourceID, err := tx.UpsertSource(ctx, record)
		if err != nil {
			return err
		}
		saved := record
		saved.ID = sourceID
		src, err := l.sourceOf(saved)
		if err != nil {
			return err
		}
		for _, skill := range skills {
			id, err := tx.FindSkill(ctx, sourceID, skill.Name)
			if err != nil {
				return err
			}
			if id != 0 {
				return existsError("fetch skill", "skill already fetched", ErrorDetails{SkillID: SkillID(id), Name: skill.Name})
			}
		}
		for _, skill := range skills {
			if skill.Name == "" {
				return fmt.Errorf("fetch skill: name is required")
			}
			description := skill.Description
			if description == "" {
				description = skillDescription(skill.Files)
			}
			createdAt := clockNow()
			skillID, versionID, err := tx.InsertSkill(ctx, sourceID, skill.Name, store.NewVersion{Description: description, Files: skill.Files, State: string(skill.State), Revision: revision, CreatedAt: createdAt})
			if err != nil {
				return err
			}
			fetched = append(fetched, Skill{ID: SkillID(skillID), Name: skill.Name, Description: description, Source: src, VersionID: VersionID(versionID), Revision: revision, FetchedAt: createdAt})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fetched, nil
}
