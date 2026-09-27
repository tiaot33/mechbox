package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

// previewTTL 是一次预览被后端回收前的存活时间;超过后视为被放弃。
const previewTTL = 30 * time.Minute

type discoveredSkill struct {
	name       string
	candidate  Candidate
	files      []File
	gitTreeSHA string       // GitHub 来源该 Skill 目录的 tree SHA;其他来源为空
	blobs      []githubBlob // GitHub 快路径:入库时按 blob SHA 取所选文件
}

// previewState 是一次预览在内存里的状态:来源规格 + 看到的远端标识 + 全部候选 skill。
// 存活于进程内存,不落库、不参与 Sync;由后续入库、Close 或 TTL 过期回收。
// source / subpath / ref / skills 建立后只读;fetching、timer 与 audits 受 previewMu 保护。
type previewState struct {
	githubRepoSize int64 // tree 给出的整仓字节数,0 表示未知或超过下载上限
	source         Source
	subpath        string
	ref            string
	httpValidator  string // URL / Store 服务端随本次读取给出的 validator;Fetch 随 Source 写入
	skills         []discoveredSkill
	diagnostics    []PreviewDiagnostic
	timer          *time.Timer
	fetching       bool                  // 一次 Fetch 正在物化 / 入库;期间句柄不可再被入库,也不会过期
	audits         map[string]SkillAudit // 按候选名缓存已确定的 skills.sh 审计;受 previewMu 保护
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
	// 预览必须拿到内容,不带条件头。
	discovered, err := l.fetchSource(ctx, source, subpath, "")
	if err != nil {
		if typed, ok := errors.AsType[*Error](err); ok && source.Type == SourceLocal && typed.Details.Field == "path" {
			typed.Details.Field = "local"
		}
		return FetchPreview{}, err
	}
	discovered.skills, err = selectSkills(discovered.skills, names)
	if err != nil {
		return FetchPreview{}, err
	}
	source.LastFetched = time.Now().UTC()
	source.LastSeenRef = discovered.ref
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
	state := &previewState{
		source: source, subpath: subpath, ref: discovered.ref, httpValidator: discovered.httpValidator,
		skills: discovered.skills, diagnostics: discovered.diagnostics, githubRepoSize: discovered.githubRepoSize,
	}
	candidates, err := l.listCandidates(ctx, state)
	if err != nil {
		return FetchPreview{}, err
	}
	l.previews[id] = state
	state.timer = time.AfterFunc(previewTTL, func() {
		l.previewMu.Lock()
		defer l.previewMu.Unlock()
		// 计时器可能在 claimPreview 停掉它之前就已触发并等在锁上:占用中的预览不回收,由 releasePreview 收尾。
		if current := l.previews[id]; current == state && !current.fetching {
			delete(l.previews, id)
		}
	})
	preview = FetchPreview{ID: id, Revision: discovered.ref, Candidates: candidates, Diagnostics: slices.Clone(state.diagnostics)}
	if source.Type == SourceGitHub {
		preview.Ref = source.Locator.Branch
	}
	return preview, nil
}

// previewSource 把入库预览的输入转成来源、来源内目录与名字条件:GitHub / URL / Local 恰好给一个。
// GitHub 输入在这里解析(见 resolveGitHubInput);本地目录的身份是其绝对路径。
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
		locator, subpath, name, err := resolveGitHubInput(ctx, *req.GitHub)
		if err != nil {
			return Source{}, "", nil, err
		}
		if name != "" {
			names = []string{name}
		}
		return Source{Type: SourceGitHub, Locator: locator}, subpath, names, nil
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

// listCandidates 按来源类型、定位与名字查询哪些候选已入库,不把结果写回预览。
func (l *sqliteLibrary) listCandidates(ctx context.Context, state *previewState) ([]Candidate, error) {
	locator, err := json.Marshal(state.source.Locator)
	if err != nil {
		return nil, fmt.Errorf("encode source locator: %w", err)
	}
	rows, err := l.db.QueryContext(ctx, `SELECT s.name, s.id FROM skills s JOIN sources src ON src.id = s.source_id WHERE src.type = ? AND src.locator = ?`, state.source.Type, locator)
	if err != nil {
		return nil, fmt.Errorf("list fetched candidates: %w", err)
	}
	defer rows.Close()
	fetched := make(map[string]SkillID)
	for rows.Next() {
		var name string
		var id SkillID
		if err := rows.Scan(&name, &id); err != nil {
			return nil, fmt.Errorf("read fetched candidate: %w", err)
		}
		fetched[name] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read fetched candidates: %w", err)
	}
	candidates := make([]Candidate, len(state.skills))
	for i, skill := range state.skills {
		candidates[i] = skill.candidate
		candidates[i].Fetched = fetched[skill.candidate.Name]
	}
	return candidates, nil
}

func (l *sqliteLibrary) Fetch(ctx context.Context, id FetchPreviewID, names []string) (fetched []SkillSummary, err error) {
	defer func() { err = typedError(err) }()
	if len(names) == 0 {
		return nil, argumentError("fetch", "names", "at least one skill name is required")
	}
	// 预览锁只用来取预览并把它标记为占用;物化(网络)与入库都在锁外,别的预览与列表不必等。
	state, skills, err := l.claimPreview(id, names)
	if err != nil {
		return nil, err
	}
	defer func() { l.releasePreview(id, state, err == nil) }()
	if state.source.Type == SourceGitHub {
		// 失败时预览仍保留完整候选,不写回部分物化的文件。
		skills = slices.Clone(skills)
		if err := l.materializeGitHubSkills(ctx, state, skills); err != nil {
			return nil, err
		}
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	created, err := l.fetchAll(ctx, state.source, skills, state.httpValidator)
	if err != nil {
		return nil, err
	}
	// 刚入库的技能还没有更新检查记录,LastUpdateCheck 为 nil。
	fetched = make([]SkillSummary, len(created))
	for i, skill := range created {
		fetched[i] = SkillSummary{ID: skill.ID, Name: skill.Name, Description: skill.Description, Source: skill.Source, FetchedAt: skill.FetchedAt, UpdatedAt: skill.UpdatedAt}
	}
	return fetched, nil
}

// claimPreview 在预览锁内找到预览、选定候选并标记占用;占用中的预览对第二次入库返回 FailedPrecondition。
// 占用期间停掉 TTL 计时器,物化途中不会被回收。
func (l *sqliteLibrary) claimPreview(id FetchPreviewID, names []string) (*previewState, []discoveredSkill, error) {
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	state := l.previews[id]
	if state == nil {
		return nil, nil, &Error{Code: NotFound, Message: "fetch: unknown or discarded preview"}
	}
	if state.fetching {
		return nil, nil, preconditionError("fetch", "another fetch from this preview is in progress", ErrorDetails{})
	}
	skills, err := selectSkills(state.skills, names)
	if err != nil {
		return nil, nil, err
	}
	state.fetching = true
	if state.timer != nil {
		state.timer.Stop()
	}
	return state, skills, nil
}

// releasePreview 结束一次占用:成功提交后丢弃句柄(同一句柄不能再次使用);失败则解除占用并重新计时,可重试。
// 预览已被 Close 清掉时无事可做。
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

func selectSkills(skills []discoveredSkill, selected []string) ([]discoveredSkill, error) {
	if len(selected) == 0 {
		return skills, nil
	}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		wanted[name] = true
	}
	result := make([]discoveredSkill, 0, len(selected))
	for _, skill := range skills {
		if wanted[skill.name] {
			result = append(result, skill)
			delete(wanted, skill.name)
		}
	}
	if len(wanted) != 0 {
		for name := range wanted {
			return nil, &Error{Code: NotFound, Message: fmt.Sprintf("source skill %q not found", name), Details: ErrorDetails{Name: name}}
		}
	}
	return result, nil
}
