package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
type fetchSession struct {
	source      Source
	subpath     string
	ref         string
	skills      []fetchedSkill
	diagnostics []CandidateDiagnostic
	timer       *time.Timer
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
	discovered, err := l.fetchRemote(ctx, source, spec.Subpath)
	if err != nil {
		return Fetch{}, err
	}
	discovered.skills, err = selectSkills(discovered.skills, spec.Names)
	if err != nil {
		return Fetch{}, err
	}
	source.LastFetched = time.Now().UTC()
	source.LastSeenRef = discovered.ref
	id, err := newFetchID()
	if err != nil {
		return Fetch{}, err
	}
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	if l.closed {
		return Fetch{}, fmt.Errorf("fetch: library is closed")
	}
	if err := ctx.Err(); err != nil {
		return Fetch{}, err
	}
	session := &fetchSession{source: source, subpath: spec.Subpath, ref: discovered.ref, skills: discovered.skills, diagnostics: discovered.diagnostics}
	l.fetches[id] = session
	session.timer = time.AfterFunc(fetchTTL, func() {
		l.fetchMu.Lock()
		defer l.fetchMu.Unlock()
		delete(l.fetches, id)
	})
	return Fetch{ID: id, Ref: discovered.ref, FetchCandidates: session.candidates()}, nil
}

func (s *fetchSession) candidates() FetchCandidates {
	result := FetchCandidates{Skills: make([]FetchSkill, len(s.skills)), Diagnostics: slices.Clone(s.diagnostics)}
	for i, skill := range s.skills {
		result.Skills[i] = skill.preview
	}
	return result
}

func (l *sqliteLibrary) ListFetchSkills(ctx context.Context, id FetchID) (result FetchCandidates, err error) {
	defer func() { err = typedError(err) }()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	session, ok := l.fetches[id]
	if !ok {
		return result, &Error{Code: NotFound, Message: "fetch session not found"}
	}
	return session.candidates(), nil
}

func (l *sqliteLibrary) InstallFetch(ctx context.Context, id FetchID, names []string) (installed []Skill, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()

	l.fetchMu.Lock()
	// 持锁直到提交并丢弃句柄,避免并发调用消费同一次 Fetch。
	defer l.fetchMu.Unlock()
	session := l.fetches[id]
	if session == nil {
		return nil, &Error{Code: NotFound, Message: "install fetch: unknown or discarded fetch"}
	}
	skills, err := selectSkills(session.skills, names)
	if err != nil {
		return nil, err
	}
	if session.source.Type == SourceGitHub {
		// 失败时会话仍保留完整候选,不写回部分物化的文件。
		skills = slices.Clone(skills)
		if err := l.materializeGitHubSkills(ctx, session, skills); err != nil {
			return nil, err
		}
	}
	installed, err = l.installAll(ctx, session.source, skills)
	if err != nil {
		return nil, err
	}
	// 成功提交入库后丢弃该 Fetch:同一句柄不能再次使用。
	if session.timer != nil {
		session.timer.Stop()
	}
	delete(l.fetches, id)
	return installed, nil
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
			return nil, &Error{Code: NotFound, Message: fmt.Sprintf("source skill %q not found", name)}
		}
	}
	return result, nil
}
