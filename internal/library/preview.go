package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// previewSession 是一次预览在内存里的状态:来源规格 + 看到的远端标识 + 全部候选 skill。
// 存活于进程内存,不落库、不参与 Sync;由后续入库、Close 或 TTL 过期回收。
// source / subpath / ref / skills 建立后只读;fetching、timer 与 audits 受 previewMu 保护。
type previewSession struct {
	githubRepoSize int64 // tree 给出的整仓字节数,0 表示未知或超过下载上限
	source         Source
	subpath        string
	ref            string
	httpValidator  string // URL / Store 服务端随本次读取给出的 validator;FetchFromPreview 随 Source 写入
	skills         []discoveredSkill
	diagnostics    []CandidateDiagnostic
	timer          *time.Timer
	fetching       bool                  // 一次 FetchFromPreview 正在物化 / 入库;期间句柄不可再被入库,也不会过期
	audits         map[string]SkillAudit // 按候选名缓存已确定的 skills.sh 审计;受 previewMu 保护
}

func (l *sqliteLibrary) PreviewSource(ctx context.Context, spec SourceSpec) (preview Preview, err error) {
	defer func() { err = typedError(err) }()
	l.previewMu.Lock()
	closed := l.closed
	l.previewMu.Unlock()
	if closed {
		return Preview{}, fmt.Errorf("preview source: library is closed")
	}
	if spec.Type == SourceLocal && spec.Locator.Path != "" {
		// 本地来源的身份是被预览目录的绝对路径,定位里的其他字段不参与。
		root, err := filepath.Abs(spec.Locator.Path)
		if err != nil {
			return Preview{}, fmt.Errorf("resolve source path: %w", err)
		}
		spec.Locator = SourceLocator{Path: root}
	}
	source := Source{Type: spec.Type, Locator: spec.Locator}
	if err := validateSource(source); err != nil {
		return Preview{}, err
	}
	if !validSubpath(spec.Subpath) || (spec.Subpath != "" && spec.Type != SourceGitHub) {
		return Preview{}, &Error{Code: InvalidArgument, Message: "invalid preview subpath"}
	}
	for _, name := range spec.Names {
		if !validSelectionName(name) {
			return Preview{}, &Error{Code: InvalidArgument, Message: "invalid preview name selection"}
		}
	}
	// 预览必须拿到内容,不带条件头。
	discovered, err := l.fetchSource(ctx, source, spec.Subpath, "")
	if err != nil {
		return Preview{}, err
	}
	discovered.skills, err = selectSkills(discovered.skills, spec.Names)
	if err != nil {
		return Preview{}, err
	}
	source.LastFetched = time.Now().UTC()
	source.LastSeenRef = discovered.ref
	id, err := newPreviewID()
	if err != nil {
		return Preview{}, err
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	if l.closed {
		return Preview{}, fmt.Errorf("preview source: library is closed")
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	session := &previewSession{
		source: source, subpath: spec.Subpath, ref: discovered.ref, httpValidator: discovered.httpValidator,
		skills: discovered.skills, diagnostics: discovered.diagnostics, githubRepoSize: discovered.githubRepoSize,
	}
	candidates, err := l.listCandidates(ctx, session)
	if err != nil {
		return Preview{}, err
	}
	l.previews[id] = session
	session.timer = time.AfterFunc(previewTTL, func() {
		l.previewMu.Lock()
		defer l.previewMu.Unlock()
		// 计时器可能在 claimPreview 停掉它之前就已触发并等在锁上:占用中的预览不回收,由 releasePreview 收尾。
		if current := l.previews[id]; current == session && !current.fetching {
			delete(l.previews, id)
		}
	})
	return Preview{ID: id, Ref: discovered.ref, PreviewCandidates: candidates}, nil
}

func (l *sqliteLibrary) ListCandidates(ctx context.Context, id PreviewID) (candidates PreviewCandidates, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := ctx.Err(); err != nil {
		return candidates, err
	}
	l.previewMu.Lock()
	session := l.previews[id]
	l.previewMu.Unlock()
	if session == nil {
		return candidates, &Error{Code: NotFound, Message: "list candidates: unknown or discarded preview"}
	}
	return l.listCandidates(ctx, session)
}

// listCandidates 在本次调用按来源类型、定位与名字查询哪些候选已入库,不把结果写回预览。
func (l *sqliteLibrary) listCandidates(ctx context.Context, session *previewSession) (PreviewCandidates, error) {
	locator, err := json.Marshal(session.source.Locator)
	if err != nil {
		return PreviewCandidates{}, fmt.Errorf("encode source locator: %w", err)
	}
	rows, err := l.db.QueryContext(ctx, `SELECT s.name, s.id FROM skills s JOIN sources src ON src.id = s.source_id WHERE src.type = ? AND src.locator = ?`, session.source.Type, locator)
	if err != nil {
		return PreviewCandidates{}, fmt.Errorf("list fetched candidates: %w", err)
	}
	defer rows.Close()
	fetched := make(map[string]SkillID)
	for rows.Next() {
		var name string
		var id SkillID
		if err := rows.Scan(&name, &id); err != nil {
			return PreviewCandidates{}, fmt.Errorf("read fetched candidate: %w", err)
		}
		fetched[name] = id
	}
	if err := rows.Err(); err != nil {
		return PreviewCandidates{}, fmt.Errorf("read fetched candidates: %w", err)
	}
	result := PreviewCandidates{Skills: make([]Candidate, len(session.skills)), Diagnostics: slices.Clone(session.diagnostics)}
	for i, skill := range session.skills {
		result.Skills[i] = skill.candidate
		result.Skills[i].Fetched = fetched[skill.candidate.Name]
	}
	return result, nil
}

func (l *sqliteLibrary) FetchFromPreview(ctx context.Context, id PreviewID, names []string) (fetched []Skill, err error) {
	defer func() { err = typedError(err) }()
	// 预览锁只用来取预览并把它标记为占用;物化(网络)与入库都在锁外,别的预览与列表不必等。
	session, skills, err := l.claimPreview(id, names)
	if err != nil {
		return nil, err
	}
	defer func() { l.releasePreview(id, session, err == nil) }()
	if session.source.Type == SourceGitHub {
		// 失败时预览仍保留完整候选,不写回部分物化的文件。
		skills = slices.Clone(skills)
		if err := l.materializeGitHubSkills(ctx, session, skills); err != nil {
			return nil, err
		}
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.fetchAll(ctx, session.source, skills, session.httpValidator)
}

// claimPreview 在预览锁内找到预览、选定候选并标记占用;占用中的预览对第二次入库返回 FailedPrecondition。
// 占用期间停掉 TTL 计时器,物化途中不会被回收。
func (l *sqliteLibrary) claimPreview(id PreviewID, names []string) (*previewSession, []discoveredSkill, error) {
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	session := l.previews[id]
	if session == nil {
		return nil, nil, &Error{Code: NotFound, Message: "fetch from preview: unknown or discarded preview"}
	}
	if session.fetching {
		return nil, nil, preconditionError("fetch from preview", "another fetch from this preview is in progress", ErrorDetails{})
	}
	skills, err := selectSkills(session.skills, names)
	if err != nil {
		return nil, nil, err
	}
	session.fetching = true
	if session.timer != nil {
		session.timer.Stop()
	}
	return session, skills, nil
}

// releasePreview 结束一次占用:成功提交后丢弃句柄(同一句柄不能再次使用);失败则解除占用并重新计时,可重试。
// 预览已被 Close 清掉时无事可做。
func (l *sqliteLibrary) releasePreview(id PreviewID, session *previewSession, fetched bool) {
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	if l.previews[id] != session {
		return
	}
	if fetched {
		delete(l.previews, id)
		return
	}
	session.fetching = false
	if session.timer != nil {
		session.timer.Reset(previewTTL)
	}
}

func newPreviewID() (PreviewID, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("issue preview handle: %w", err)
	}
	return PreviewID(hex.EncodeToString(buffer)), nil
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
