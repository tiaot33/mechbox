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

// PreviewFetch 发起一次入库预览:发现来源中的候选 skill,返回句柄与候选清单,此时尚无内容进入 Library。
// req 的 GitHub / URL / Local 恰好给一个,多给或全空返回 InvalidArgument:多给时 Details.Field 指向多出的字段,全空时为空。
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

func (l *Library) previewSource(ctx context.Context, req PreviewFetchRequest) (source Source, subpath string, names []string, err error) {
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

func (l *Library) listCandidates(ctx context.Context, state *previewState) ([]Candidate, error) {
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

// Fetch 从一次入库预览中选取候选原子提交入库,返回入库技能的摘要(不含文件,需要时用 GetSkill)。
// names 为空返回 InvalidArgument;"全部入库"由调用方把 Fetched 为零的候选名收集起来传入。
// 所选技能中有已入库的,整批返回 AlreadyExists。
// GitHub 来源可在入库预览已钉住的 commit 上联网取所选 Skill 文件,不重新解析 ref,
// 不跟随当前 HEAD。URL / 本地目录只读取入库预览时的内容,不再读取来源。要么所选 skill 全部入库,
// 要么库保持不变。成功后该入库预览句柄被丢弃(含未选候选),再次使用返回 NotFound;
// 失败时保留句柄供重试,直至 Close 或过期。同一句柄的一次入库尚在进行时,再次入库返回
// FailedPrecondition;联网期间不持库锁,其他入库预览 / 列表 / 安装不必等它。
func (l *Library) Fetch(ctx context.Context, id FetchPreviewID, names []string) (fetched []SkillSummary, err error) {
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
