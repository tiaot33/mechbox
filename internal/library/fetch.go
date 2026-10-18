package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/market"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/download"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/git"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/wellknown"
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

// PreviewFetchRequest 是入库预览的输入:Input / Collection 恰好给一个。
type PreviewFetchRequest struct {
	// Input 是用户粘贴的来源,由入库预览识别是哪一种(见 PreviewFetch):本地目录(绝对路径或以 ./、../ 开头的相对路径)、
	// GitHub 仓库(owner/repo、owner/repo@name、HTTPS / SSH 仓库地址、tree URL,可带 #ref;@name 时入库预览只列该技能)、
	// GitHub / GitLab 的 raw、archive、release 文件链接、GitLab 链接与 gitlab: 简写、SSH 与以 .git 结尾的 Git 地址,以及其他 http(s) 链接。
	// 以域名开头、没写协议的输入(github.com/owner/repo、git.company.com/org/repo)先补上 https:// 再识别。
	Input   string `json:"input,omitempty"`
	Ref     string `json:"ref,omitempty"`     // 显式分支或 tag,只对 Git 类来源(GitHub 仓库、Git 仓库)有意义;带斜杠的必须从这里传;省略时发现默认分支
	Subpath string `json:"subpath,omitempty"` // 仓库内目录,只对 Git 类来源有意义
	// Collection 对库里已有的集合再做一次入库预览,用于看作者又发布了哪些技能:按入库时识别出的来源类型与定位信息重新读取,不重新识别,
	// 候选上的 Fetched 标出哪些已经入库,New 标出新技能。入库时指定的子目录没有记下来,Git 类来源看的是整个仓库(分支不变)。
	Collection CollectionID `json:"collection,omitzero"`
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
	ID FetchPreviewID `json:"id"`
	// Source 是识别结果:来源类型、展示名称,以及 Git 类来源实际使用的分支或 tag(省略时为发现的默认分支)。
	// 与入库后集合和技能上的 Source 相同。
	Source      Source              `json:"source"`
	Revision    string              `json:"revision"` // 钉住的内容:Git 类来源为 commit,well-known 为索引内容哈希,直接下载为文件内容哈希,本地目录为空
	Candidates  []Candidate         `json:"candidates"`
	Diagnostics []PreviewDiagnostic `json:"diagnostics,omitempty"`
}

// Candidate 是入库预览中可挑选入库的候选;skills.sh 审计不随候选返回,见 GetSkillAudit。
type Candidate struct {
	Fetched SkillID `json:"fetched,omitzero"` // 同一来源同名的库内技能 ID;入库预览时查询,零值表示未入库
	// New 为 true 表示这是来源里的新技能:库里已有这个来源的集合,而这个技能是用户上次从这个来源入库之后才出现的,
	// 用户也没对它点过「不感兴趣」。新出现的技能默认勾上由界面决定。
	New         bool   `json:"new,omitzero"`
	PluginName  string `json:"plugin_name,omitempty"` // 清单显式声明的插件分组名,仅在入库预览中展示
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"` // 相对来源根目录
}

// PreviewDiagnostic 说明一个路径为何未成为候选。
type PreviewDiagnostic struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// SearchSkills 只读搜索 skills.sh,最多返回 20 项;服务失败与空结果分别返回。
// 来源是 GitHub 仓库的条目转成 owner/repo@name,来源是 well-known 站点的条目转成该站点 well-known 下这个技能的链接;
// 其他无法转换为入库预览输入的条目直接过滤掉。
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
		input, _, ok := searchInputs(skill)
		if !ok {
			continue
		}
		result = append(result, SearchSkill{ID: skill.ID, Name: skill.Name, DetailURL: skill.DetailURL, Installs: skill.Installs, Preview: PreviewFetchRequest{Input: input}})
	}
	return result, nil
}

func searchInputs(skill market.Skill) (skillInput, sourceInput string, ok bool) {
	if !discovery.ValidName(skill.Name) || strings.Contains(skill.Name, "#") {
		return "", "", false
	}
	if repository, ok := github.PlainRepository(skill.Source); ok {
		return repository + "@" + skill.Name, repository, true
	}
	link, ok := wellknown.SearchInput(skill.Source, skill.Name)
	if !ok {
		return "", "", false
	}
	u, err := url.Parse(link)
	if err != nil {
		return "", "", false
	}
	return link, u.Scheme + "://" + u.Host, true
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

// PreviewFetch 发起一次入库预览:识别来源,发现其中的候选 skill,返回句柄、识别结果与候选清单,此时尚无内容进入 Library。
// req 的 Input / Collection 恰好给一个,都给或都空返回 InvalidArgument:都给时 Details.Field 为 collection,都空时为空;
// Ref / Subpath 用在不是 Git 类来源的输入或 Collection 上时返回 InvalidArgument,Details.Field 为 ref / subpath;
// 输入本身无效时 Details.Field 为 input;集合不存在返回 NotFound。
//
// Input 按下面的顺序识别,只在入库预览时识别一次;入库后集合记住识别出的来源类型,检查更新与更新都按它读取。
// 以域名开头(第一段带点,如 github.com/…、git.company.com/…)、没写协议的输入先补上 https://,再按这个顺序识别;
// owner/repo 简写的第一段不带点,不受影响:
//  1. 本地路径(绝对路径,或以 ./、../ 开头的相对路径;不带前缀的 a/b 按 GitHub 简写处理):本地目录。相对路径按当前工作目录解析为绝对路径。
//  2. GitHub / GitLab 的 raw、archive、release 文件链接(raw.githubusercontent.com、codeload.github.com、github.com/…/raw|archive|releases/download/…、
//     …/-/raw/…、…/-/archive/…、…/-/releases/…/downloads/…):直接下载,不克隆仓库。
//  3. GitHub 的简写和仓库链接:GitHub 仓库。输入无效、tree URL 的 ref 边界有歧义、#ref 与 Ref 不一致、Subpath 与 tree URL
//     路径不一致返回 InvalidArgument;@name 时入库预览只列该技能,不存在返回 NotFound。
//  4. GitLab 链接(gitlab.com/group/subgroup/repo、带 /-/tree/<ref>/<path> 的自托管实例链接)、gitlab: 简写、SSH 地址(git@host:path、ssh://…)
//     与以 .git 结尾的 http(s) 地址:Git 仓库,从链接里拆出分支和仓库内目录,用这台机器已有的 git 凭据(SSH key、credential helper)读取。
//  5. 其他 http(s) 链接:先请求链接本身,内容是带 frontmatter 的 SKILL.md、zip、tar 或 tar.gz(按内容判断,不看扩展名)时直接下载;
//     否则在链接所在路径下找 well-known 索引(先 .well-known/agent-skills/index.json,再旧的 .well-known/skills/index.json),
//     链接是某个 well-known 技能的地址(…/.well-known/agent-skills/<name>)时入库预览只列这个技能;还不是时试一次 git ls-remote,
//     能连上就当 Git 仓库。都不是时返回 InvalidArgument。链接里带用户名或密码时直接返回 InvalidArgument,不联网,以免凭据写进库里。链接带路径时只收这个路径下的技能:路径下没有索引、站点根下有时返回 NotFound,
//     Details.Name 为这个路径,不扩大到整个站点。链接本身连不上或返回 5xx 时返回 SourceUnreachable。
//
// 省略 Ref 时发现默认分支,失败返回 SourceUnreachable,不假定 main。
// 入库预览钉住来源当时的内容:GitHub 只取各候选 SKILL.md 并钉住当时的 commit;Git 仓库浅克隆一次并钉住 commit;
// 直接下载一次读完整个文件;well-known 钉住索引,技能文件在入库时按索引读取;本地目录把候选文件读进内存作为快照
// (候选文件累计超过 1000 个或 64 MiB 返回 SourceInvalid),之后目录再被改动也不影响入库。本地来源的身份是该目录的绝对路径,
// 同一目录下的技能归入同一 Collection。一个有效候选都没有时返回 SourceInvalid,诊断写在错误信息里。
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
	target, field, err := l.previewTarget(ctx, req)
	if err != nil {
		return FetchPreview{}, inputField(err, field)
	}
	var candidates []discovery.Candidate
	var diagnostics []discovery.Diagnostic
	snapshot := target.Snapshot
	if snapshot == nil {
		snapshot, err = l.sources.Open(ctx, target.Type, target.Locator, target.Subpath, nil)
	}
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
	src, err := l.sourceOf(record)
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
	return FetchPreview{ID: id, Source: src, Revision: snapshot.Revision(), Candidates: listed, Diagnostics: previewDiagnostics(state.diagnostics)}, nil
}

func (l *Library) previewTarget(ctx context.Context, req PreviewFetchRequest) (target source.Target, field string, err error) {
	input := strings.TrimSpace(req.Input)
	switch {
	case input == "" && req.Collection == 0:
		return source.Target{}, "", argumentError("preview fetch", "", "one of input or collection is required")
	case input != "" && req.Collection != 0:
		return source.Target{}, "collection", argumentError("preview fetch", "collection", "only one of input or collection may be given")
	case req.Collection != 0:
		if err := gitOnly(req); err != nil {
			return source.Target{}, "", err
		}
		target, err = l.collectionTarget(ctx, req.Collection)
		return target, "collection", err
	}
	input = withHTTPS(input)
	switch {
	case local.Matches(input):
		if err := gitOnly(req); err != nil {
			return source.Target{}, "", err
		}
		target, err = local.Locate(input)
	case download.Hosted(input):
		if err := gitOnly(req); err != nil {
			return source.Target{}, "", err
		}
		target, err = download.Locate(input)
	case github.Matches(input):
		target, err = github.Locate(ctx, github.Input{Input: input, Ref: req.Ref, Subpath: req.Subpath})
	case git.Matches(input):
		target, err = git.Locate(ctx, git.Input{Input: input, Ref: req.Ref, Subpath: req.Subpath})
	case webLink(input):
		target, err = l.probeLink(ctx, input, req)
	default:
		err = &source.InputError{Reason: "unrecognized source input; expected a local path, a GitHub repository, a Git repository or an http(s) link"}
	}
	return target, "input", err
}

var bareHost = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+(?::[0-9]+)?$`)

func withHTTPS(input string) string {
	host, _, _ := strings.Cut(input, "/")
	host, _, _ = strings.Cut(host, "#")
	if strings.Contains(input, "://") || !bareHost.MatchString(host) {
		return input
	}
	return "https://" + input
}

// SourceRefs 是入库预览之前给用户选分支用的:一个 Git 类来源的全部分支和 tag。
type SourceRefs struct {
	Branches      []string `json:"branches"`            // 全部分支,按名字排序;带斜杠的(如 release/v2)原样列出
	Tags          []string `json:"tags"`                // 全部 tag,按名字排序
	DefaultBranch string   `json:"default_branch"`      // 来源的默认分支;来源没报出默认分支(如远端 HEAD 游离)时为空
	InputRef      string   `json:"input_ref,omitempty"` // 输入本身带的分支或 tag(tree 链接、#ref),界面据此选中;没带或 tree 链接里分不清是哪个时为空
}

// ListSourceRefs 列出用户输入指向的 Git 类来源的全部分支和 tag,供界面在入库预览之前给出分支下拉,选好再扫描。
// 只连一次来源,不做入库预览、不读技能文件、不克隆;访问方式与入库预览解析分支时相同(git ls-remote,用这台机器已有的 git 凭据),
// 私有仓库能不能读也与入库预览一致。
//
// 只接受从输入就能认出的 Git 类来源:GitHub 简写和链接、GitLab 链接与 gitlab: 简写、SSH 地址、以 .git 结尾的地址,
// 以及补上 https:// 后属于这几类的输入(补协议的规则与 PreviewFetch 相同)。tree 链接里带斜杠的分支按分支列表认出,
// 如 …/tree/release/v2/skills 的 InputRef 为 release/v2。
// 其他输入(本地路径、下载链接、要先请求链接内容才能判断的 http(s) 链接)与无效输入(含无效的 #ref、tree 链接与 #ref 不一致,规则与 PreviewFetch 相同)
// 返回 InvalidArgument,Details.Field 为 input,不联网。
// 连不上、没有权限、仓库不存在时按与入库预览相同的错误码返回。
func (l *Library) ListSourceRefs(ctx context.Context, input string) (refs SourceRefs, err error) {
	defer func() { err = typedError(err) }()
	input = withHTTPS(strings.TrimSpace(input))
	var listed git.Refs
	switch {
	case input == "":
		return SourceRefs{}, argumentError("list source refs", "input", "input is required")
	case local.Matches(input), download.Hosted(input):
		return SourceRefs{}, argumentError("list source refs", "input", "branches and tags are only listed for Git repositories")
	case github.Matches(input):
		listed, err = github.InputRefs(ctx, input)
	case git.Matches(input):
		listed, err = git.InputRefs(ctx, input)
	default:
		return SourceRefs{}, argumentError("list source refs", "input", "branches and tags are only listed for inputs recognizable as Git repositories")
	}
	if err != nil {
		return SourceRefs{}, inputField(err, "input")
	}
	return SourceRefs{Branches: listed.Branches, Tags: listed.Tags, DefaultBranch: listed.DefaultBranch, InputRef: listed.InputRef}, nil
}

func gitOnly(req PreviewFetchRequest) error {
	switch {
	case req.Ref != "":
		return argumentError("preview fetch", "ref", "ref only applies to Git repositories")
	case req.Subpath != "":
		return argumentError("preview fetch", "subpath", "subpath only applies to Git repositories")
	}
	return nil
}

func webLink(input string) bool {
	u, err := url.Parse(input)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func (l *Library) probeLink(ctx context.Context, link string, req PreviewFetchRequest) (source.Target, error) {
	link, _, _ = strings.Cut(link, "#")
	if u, err := url.Parse(link); err != nil || u.User != nil {
		return source.Target{}, &source.InputError{Reason: "links with embedded credentials are not supported; configure Git credentials on this machine instead"}
	}
	target, ok, err := l.download.Probe(ctx, link)
	if err != nil {
		return source.Target{}, err
	}
	if !ok {
		target, ok, err = wellknown.Locate(ctx, link)
	}
	scope, scoped := errors.AsType[*wellknown.ScopeError](err)
	if err != nil && !scoped {
		return source.Target{}, err
	}
	if ok {
		return target, gitOnly(req)
	}
	target, ok, err = git.Probe(ctx, link, req.Ref, req.Subpath)
	switch {
	case err != nil:
		return source.Target{}, err
	case ok:
		return target, nil
	case scoped:
		return source.Target{}, scope
	}
	return source.Target{}, &source.InputError{Reason: "link is neither a skill file or archive, nor a site with a well-known skills index, nor a Git repository"}
}

func (l *Library) collectionTarget(ctx context.Context, id CollectionID) (source.Target, error) {
	if id < 0 {
		return source.Target{}, argumentError("preview fetch", "collection", "collection ID must be positive")
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	row, err := l.store.GetCollectionSource(ctx, int64(id))
	if errors.Is(err, store.ErrNotFound) {
		return source.Target{}, &Error{Code: NotFound, Message: "preview fetch: collection not found", Err: err}
	}
	if err != nil {
		return source.Target{}, err
	}
	return source.Target{Type: row.Type, Locator: row.Locator}, nil
}

func inputField(err error, field string) error {
	if _, ok := errors.AsType[*source.InputError](err); ok {
		return &Error{Code: InvalidArgument, Message: err.Error(), Details: ErrorDetails{Field: field}, Err: err}
	}
	return err
}

func (l *Library) listCandidates(ctx context.Context, state *previewState) ([]Candidate, error) {
	known, err := l.store.SourceSkills(ctx, state.source.Type, state.source.Locator)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, len(state.candidates))
	for i, c := range state.candidates {
		fetched := known.Fetched[c.Name]
		isNew := len(known.Fetched) > 0 && fetched == 0 && !known.Seen[c.Name]
		candidates[i] = Candidate{Fetched: SkillID(fetched), New: isNew, PluginName: c.PluginName, Name: c.Name, Description: c.Description, Path: c.Path}
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

// Fetch 从一次入库预览中选取候选原子提交入库,返回入库的技能。入库同时按整个来源(不限入库预览的子目录,
// 也不限从 skills.sh 搜到的那一个)记下来源里当时有哪些技能,都算用户见过:以后只有来源里新出现的才算新技能。
// 子目录入库时为此要多读一次整个来源,读不成时入库失败、可重试。
// names 为空返回 InvalidArgument;"全部入库"由调用方把 Fetched 为零的候选名收集起来传入。
// 所选技能中有已入库的,整批返回 AlreadyExists。
// GitHub 来源可在入库预览已钉住的 commit 上联网取所选 Skill 文件,不重新解析 ref,不跟随当前 HEAD;
// Git 仓库、直接下载与本地目录只读取入库预览时的内容,不再读取来源;well-known 按入库预览钉住的索引读取所选技能的文件。
// 内容取不全时不收残缺的版本:一个技能缺了任何一个文件、与索引里的 digest 不一致、SKILL.md 取不到或缺 name / description,
// 整批失败,错误的 Details.Name 为这个技能,Details.Path 为取不到的文件(不是某个文件的问题时为空)。
// 要么所选 skill 全部入库,要么库保持不变。成功后该入库预览句柄被丢弃(含未选候选),再次使用返回 NotFound;
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
	seen, err := state.snapshot.Names(ctx)
	if err != nil {
		return nil, err
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.fetchAll(ctx, state.source, state.snapshot.Revision(), skills, append(seen, names...))
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
// 结果随入库预览缓存,AuditUnavailable 不缓存、再次调用会重查。不是 GitHub 仓库的来源(Git 仓库、well-known、直接下载、本地目录)
// 不查审计,返回 nil 且不联网。未知或已失效句柄、不在候选中的名字返回 NotFound。
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

func (l *Library) fetchAll(ctx context.Context, record store.Source, revision string, skills []source.Skill, seen []string) ([]Skill, error) {
	fetched := make([]Skill, 0, len(skills))
	err := l.store.InTx(ctx, func(tx *store.Tx) error {
		sourceID, err := tx.UpsertSource(ctx, record)
		if err != nil {
			return err
		}
		if err := tx.ListSourceSkills(ctx, sourceID, seen, revision, true); err != nil {
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
			fetched = append(fetched, Skill{ID: SkillID(skillID), Name: skill.Name, Description: description, CollectionID: CollectionID(sourceID), Source: src, VersionID: VersionID(versionID), Revision: revision, FetchedAt: createdAt})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fetched, nil
}
