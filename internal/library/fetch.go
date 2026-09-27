package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// fetchTTL 是一次 Fetch 暂存被后端回收前的存活时间;超过后视为被放弃。
const fetchTTL = 30 * time.Minute

type fetchedSkill struct {
	name       string
	preview    FetchSkill
	files      []File
	gitTreeSHA string       // GitHub 来源该 Skill 目录的 tree SHA;其他来源为空
	blobs      []githubBlob // GitHub 快路径:安装时按 blob SHA 取所选文件
}

// fetchSession 是一次 Fetch 抓取的内存暂存:来源规格 + 看到的远端标识 + 全部候选 skill。
// 存活于进程内存,不落库、不参与 Sync;由后续安装、Close 或 TTL 过期回收。
// source / subpath / ref / skills 建立后只读;installing 与 timer 受 fetchMu 保护。
type fetchSession struct {
	githubRepoSize int64 // tree 给出的整仓字节数,0 表示未知或超过下载上限
	source         Source
	subpath        string
	ref            string
	httpValidator  string // URL / Store 服务端随本次抓取给出的 validator;InstallFetch 随 Source 写入
	skills         []fetchedSkill
	diagnostics    []CandidateDiagnostic
	timer          *time.Timer
	installing     bool // 一次 InstallFetch 正在物化 / 入库;期间句柄不可再被安装,也不会过期
}

func (l *sqliteLibrary) Fetch(ctx context.Context, spec SourceSpec) (fetch Fetch, err error) {
	defer func() { err = typedError(err) }()
	l.fetchMu.Lock()
	closed := l.closed
	l.fetchMu.Unlock()
	if closed {
		return Fetch{}, fmt.Errorf("fetch: library is closed")
	}
	if spec.Type == SourceLocal {
		return Fetch{}, &Error{Code: InvalidArgument, Message: "fetch: local sources must use Install"}
	}
	source := Source{Type: spec.Type, Locator: spec.Locator}
	if err := validateSource(source); err != nil {
		return Fetch{}, err
	}
	if !validSubpath(spec.Subpath) || (spec.Subpath != "" && spec.Type != SourceGitHub) {
		return Fetch{}, &Error{Code: InvalidArgument, Message: "invalid Fetch subpath"}
	}
	for _, name := range spec.Names {
		if !validSelectionName(name) {
			return Fetch{}, &Error{Code: InvalidArgument, Message: "invalid Fetch name selection"}
		}
	}
	// Fetch 必须拿到内容,不带条件头。
	discovered, err := l.fetchRemote(ctx, source, spec.Subpath, "")
	if err != nil {
		return Fetch{}, err
	}
	discovered.skills, err = selectSkills(discovered.skills, spec.Names)
	if err != nil {
		return Fetch{}, err
	}
	// 审计只查筛选后的候选;URL / Store 与非 github.com 仓库不在 skills.sh 收录范围,不查。
	if owner, repo, ok := githubOwnerRepo(source.Locator.Repository); ok {
		attachAudits(ctx, owner, repo, discovered.skills)
	}
	source.LastFetched = time.Now().UTC()
	source.LastSeenRef = discovered.ref
	id, err := newFetchID()
	if err != nil {
		return Fetch{}, err
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	if l.closed {
		return Fetch{}, fmt.Errorf("fetch: library is closed")
	}
	if err := ctx.Err(); err != nil {
		return Fetch{}, err
	}
	session := &fetchSession{
		source: source, subpath: spec.Subpath, ref: discovered.ref, httpValidator: discovered.httpValidator,
		skills: discovered.skills, diagnostics: discovered.diagnostics, githubRepoSize: discovered.githubRepoSize,
	}
	candidates, err := l.fetchCandidates(ctx, session)
	if err != nil {
		return Fetch{}, err
	}
	l.fetches[id] = session
	session.timer = time.AfterFunc(fetchTTL, func() {
		l.fetchMu.Lock()
		defer l.fetchMu.Unlock()
		// 计时器可能在 claimFetch 停掉它之前就已触发并等在锁上:占用中的会话不回收,由 releaseFetch 收尾。
		if current := l.fetches[id]; current == session && !current.installing {
			delete(l.fetches, id)
		}
	})
	return Fetch{ID: id, Ref: discovered.ref, FetchCandidates: candidates}, nil
}

func (l *sqliteLibrary) ListFetchSkills(ctx context.Context, id FetchID) (candidates FetchCandidates, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := ctx.Err(); err != nil {
		return candidates, err
	}
	l.fetchMu.Lock()
	session := l.fetches[id]
	l.fetchMu.Unlock()
	if session == nil {
		return candidates, &Error{Code: NotFound, Message: "list fetch skills: unknown or discarded fetch"}
	}
	return l.fetchCandidates(ctx, session)
}

// fetchCandidates 在本次调用查询来源类型、定位与名字,不把安装标记写回会话。
func (l *sqliteLibrary) fetchCandidates(ctx context.Context, session *fetchSession) (FetchCandidates, error) {
	locator, err := json.Marshal(session.source.Locator)
	if err != nil {
		return FetchCandidates{}, fmt.Errorf("encode source locator: %w", err)
	}
	rows, err := l.db.QueryContext(ctx, `SELECT s.name, s.id FROM skills s JOIN sources src ON src.id = s.source_id WHERE src.type = ? AND src.locator = ?`, session.source.Type, locator)
	if err != nil {
		return FetchCandidates{}, fmt.Errorf("list installed candidates: %w", err)
	}
	defer rows.Close()
	installed := make(map[string]SkillID)
	for rows.Next() {
		var name string
		var id SkillID
		if err := rows.Scan(&name, &id); err != nil {
			return FetchCandidates{}, fmt.Errorf("read installed candidate: %w", err)
		}
		installed[name] = id
	}
	if err := rows.Err(); err != nil {
		return FetchCandidates{}, fmt.Errorf("read installed candidates: %w", err)
	}
	result := FetchCandidates{Skills: make([]FetchSkill, len(session.skills)), Diagnostics: slices.Clone(session.diagnostics)}
	for i, skill := range session.skills {
		result.Skills[i] = skill.preview
		result.Skills[i].Audit = cloneAudit(skill.preview.Audit)
		result.Skills[i].Installed = installed[skill.preview.Name]
	}
	return result, nil
}

func (l *sqliteLibrary) InstallFetch(ctx context.Context, id FetchID, names []string) (installed []Skill, err error) {
	defer func() { err = typedError(err) }()
	// 会话锁只用来取会话并把它标记为占用;物化(网络)与入库都在锁外,别的 Fetch 与列表不必等。
	session, skills, err := l.claimFetch(id, names)
	if err != nil {
		return nil, err
	}
	defer func() { l.releaseFetch(id, session, err == nil) }()
	if session.source.Type == SourceGitHub {
		// 失败时会话仍保留完整候选,不写回部分物化的文件。
		skills = slices.Clone(skills)
		if err := l.materializeGitHubSkills(ctx, session, skills); err != nil {
			return nil, err
		}
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.installAll(ctx, session.source, skills, session.httpValidator)
}

// claimFetch 在会话锁内找到会话、选定候选并标记占用;占用中的会话对第二次安装返回 FailedPrecondition。
// 占用期间停掉 TTL 计时器,物化途中不会被回收。
func (l *sqliteLibrary) claimFetch(id FetchID, names []string) (*fetchSession, []fetchedSkill, error) {
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	session := l.fetches[id]
	if session == nil {
		return nil, nil, &Error{Code: NotFound, Message: "install fetch: unknown or discarded fetch"}
	}
	if session.installing {
		return nil, nil, preconditionError("install fetch", "another install of this fetch is in progress", ErrorDetails{})
	}
	skills, err := selectSkills(session.skills, names)
	if err != nil {
		return nil, nil, err
	}
	session.installing = true
	if session.timer != nil {
		session.timer.Stop()
	}
	return session, skills, nil
}

// releaseFetch 结束一次占用:成功提交后丢弃句柄(同一句柄不能再次使用);失败则解除占用并重新计时,可重试。
// 会话已被 Close 清掉时无事可做。
func (l *sqliteLibrary) releaseFetch(id FetchID, session *fetchSession, installed bool) {
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	if l.fetches[id] != session {
		return
	}
	if installed {
		delete(l.fetches, id)
		return
	}
	session.installing = false
	if session.timer != nil {
		session.timer.Reset(fetchTTL)
	}
}

func newFetchID() (FetchID, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("issue fetch handle: %w", err)
	}
	return FetchID(hex.EncodeToString(buffer)), nil
}

func selectSkills(skills []fetchedSkill, selected []string) ([]fetchedSkill, error) {
	if len(selected) == 0 {
		return skills, nil
	}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		wanted[name] = true
	}
	result := make([]fetchedSkill, 0, len(selected))
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
