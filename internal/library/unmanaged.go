package library

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/market"
	"github.com/tiaot33/mechbox/internal/library/internal/process"
	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/source/catalog"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// UnmanagedSkill 是作用域的技能目录里一个不是库装的技能文件夹:这个作用域里没有安装记录用它。它可能是命令行工具
// (如 npx skills)装的、手动拷进去的、用户自己写的,或者提交在项目仓库里的。库只列出它,不改动、不删除。
// 和库里某个技能的某一版完全一致的文件夹列出时自动记为那个技能的安装,不再是未纳管技能(不自动纳入的几种见 SkillID)。
type UnmanagedSkill struct {
	Scope Scope `json:"scope"`
	// Directory 是文件夹所在的技能目录,写法同 [SkillScope].Directory;同一个 Agent 的几个目录里可以有同名的文件夹,按它区分。
	Directory string `json:"directory"`
	// Name 是文件夹名。
	Name string `json:"name"`
	// Description 取自 SKILL.md 的 frontmatter;frontmatter 读不出时为空。
	Description string `json:"description,omitempty"`
	// Folder 是技能文件夹在作用域目录里的路径;文件夹是符号链接时就是链接本身,「在 Finder 中显示」用它。
	Folder string `json:"folder"`
	// LinkTarget 在文件夹是符号链接时给出它最终指向的目录(已解析各级符号链接),否则为空。
	// 例如 npx skills 把真实文件夹放在 ~/.agents/skills/<name>,再从其他 Agent 的目录用符号链接指过去。
	LinkTarget string `json:"link_target,omitempty"`
	// LinkOutside 表示 LinkTarget 不在任何 Agent 的技能目录里(表里各 Agent 的全部全局目录与在各已登记项目里的全部技能目录,候选池里的也算),
	// 例如技能作者把它链到自己的工作目录 ~/code/my-skills/foo,在那里改了 Agent 马上就能用上。入库后库第一次写这一处时
	// 会把链接换成独立的一份,在原来的目录里改就不再影响这里;所以和在项目仓库里的一样默认不入库,内容和库里一致也不自动纳入,
	// 界面写明它指向哪里。
	LinkOutside bool `json:"link_outside,omitzero"`
	// InRepository 表示文件夹已提交在项目的 git 仓库里(在 HEAD 里),界面标明「在项目仓库里」。只出现在项目作用域;
	// 项目不是 git 仓库、还没有提交或本机没有 git 时为 false。
	InRepository bool `json:"in_repository,omitzero"`
	// SourceInput 是从 npx skills 留下的来源记录里认出的来源,格式同 PreviewFetchRequest.Input;入库确认里预填给
	// FetchUnmanagedRequest.SourceInput,界面写「来自 …」。认不出时为空,用户可以自己给一个来源链接。
	// 全局作用域读 ~/.agents/.skill-lock.json(不跟随 XDG_STATE_HOME,只认 version 3 及以上),
	// 项目作用域读项目根目录的 skills-lock.json;记录按技能名对上文件夹:先比 SKILL.md 里的 name,再比文件夹名。
	// 记录里的来源类型为 github、gitlab、git、well-known、download 与 local(项目里记录的相对路径按项目根解析)时认得出,其他的认不出。
	// SkillID 不为 0 时,那个技能有来源则为空:它就是库里的那个技能,跟着那个技能的来源,来源记录让位于它;
	// 那个技能没有来源时照样给出,入库不读它(只补记安装),界面据此提示可以把这个来源接到那个技能上(AttachSource)。
	SourceInput string `json:"source_input,omitempty"`
	// SourceRef 是来源记录里记的分支或 tag,只对 Git 类来源(github、gitlab、git)有;预填给 FetchUnmanagedRequest.SourceRef。
	SourceRef string `json:"source_ref,omitempty"`
	// InProjectLock 表示项目根目录的 skills-lock.json 记了这个技能(认不认得出来源都算):技能是团队约定装的,文件夹本身不一定提交了,
	// 和 InRepository 一样归项目仓库管。只出现在项目作用域。
	InProjectLock bool `json:"in_project_lock,omitzero"`
	// SkillID 不为 0 时,文件夹的内容(文件路径与内容都相同,不比可执行位;符号链接按它指向的文件夹)和库里这个技能的某个版本一致,
	// VersionID 是那个版本:和当前版本一致的优先,其次是最新的旧版本;库里几个技能都有这样一版时取同一个,不看这个作用域装没装过哪个。
	// 这样的文件夹列出时(ListUnmanagedSkills、PlanFetchUnmanaged)自动记为这个技能装在这里,不再出现在结果里;留下的有不自动纳入的几种:
	// 提交在项目仓库里的(InRepository)、记在 skills-lock.json 里的(InProjectLock)、指向 Agent 目录以外的符号链接(LinkOutside),
	// 入库(FetchUnmanaged、FetchUnmanagedSkills)只给这个技能补记这一处安装,不论带不带来源、带的是哪个来源;
	// 还有这个作用域已经装着这个技能时,同一个作用域里另一份一样的文件夹:一个技能在一个作用域只记一处安装,入库不另记,整批入库的计划里没有它。
	// 它们就是库里的这个技能,只是这一处不是库装的,界面把它显示在这个技能下。
	SkillID   SkillID   `json:"skill_id,omitzero"`
	VersionID VersionID `json:"version_id,omitzero"`
	// SourceLatestOf 不为 0 时,文件夹的内容和库里这个技能在来源里现在的那一版完全一致(最近一次检查更新看到的、比库里新的那一版):
	// 是这个技能的新版,在库外更新过。按来源入库会因集合里已有同名技能返回 AlreadyExists;界面引导用更新(PlanUpdate、Update)
	// 把库里的技能换成这一版,之后这一处和库里的当前版本一致,SkillID 就是它。SkillID 不为 0 时为 0。
	SourceLatestOf SkillID `json:"source_latest_of,omitzero"`
	// SameFolder 是别的作用域里和这一处解析符号链接后是同一个文件夹的未纳管技能(如 npx skills 的布局:Claude Code 的目录里是链接,
	// 真实文件夹在 Codex 的目录里),按 Agent、作用域排序;入库这一处时(FetchUnmanaged、FetchUnmanagedSkills)一并记为装在那里,
	// 确认前界面据此说明。不限于这次列出的作用域,读不出来的作用域不算;共用目录的 Agent、归项目仓库管的那一处(InRepository、
	// InProjectLock)不在这里,入库时也不一并记下。没有时为空。
	SameFolder []Scope `json:"same_folder,omitempty"`
	// Invalid 不为 nil 时,文件夹根上的 SKILL.md 不是有效的技能描述,给出哪里写错了(SKILL.md 超出列出时的读取上限时不判断):这个文件夹不能入库
	// (FetchUnmanaged 返回 SourceInvalid,整批入库的计划里这一条带 Error),界面在作用域里就据此说明,不给入库的按钮。
	Invalid *SkillFileProblem `json:"invalid,omitempty"`
	// AgentSettings 是文件夹里的技能对 Claude Code、Codex 声明的设置(见 [AgentSettings]);都是默认时为 nil。
	AgentSettings *AgentSettings `json:"agent_settings,omitempty"`
}

// SkillFileProblemKind 是 SKILL.md 不是有效技能描述的原因。值以后可能增加,调用方遇到不认识的值按「SKILL.md 写得不对」处理。
type SkillFileProblemKind string

const (
	SkillFileNoFrontmatter       SkillFileProblemKind = "no_frontmatter"       // 开头没有 frontmatter:第一行不是 ---
	SkillFileUnclosedFrontmatter SkillFileProblemKind = "unclosed_frontmatter" // frontmatter 没有用 --- 结束
	SkillFileInvalidYAML         SkillFileProblemKind = "invalid_yaml"         // frontmatter 不是有效的 YAML
	// SkillFileUnquotedColon 是 frontmatter 里一个值中间带「: 」又没加引号(如 description: Use when: ...),YAML 把它读成了又一层键值。
	SkillFileUnquotedColon SkillFileProblemKind = "unquoted_colon"
	SkillFileInvalidName   SkillFileProblemKind = "invalid_name"   // 没有 name,或 name 不能用作文件夹名(空、带斜杠或换行)
	SkillFileNoDescription SkillFileProblemKind = "no_description" // 没有 description,或它不是一段文字
)

// SkillFileProblem 说明技能文件夹里的 SKILL.md 为什么不是有效的技能描述,界面据此写出哪个文件、哪里写错了。
type SkillFileProblem struct {
	Kind SkillFileProblemKind `json:"kind"`
	Line int                  `json:"line,omitempty"` // 写错的那一行在 SKILL.md 里的行号,从 1 起;说不出时为 0
}

func skillFileProblem(err error) *SkillFileProblem {
	problem, ok := errors.AsType[*discovery.SkillMDError](err)
	if !ok {
		return &SkillFileProblem{Kind: SkillFileInvalidYAML}
	}
	return &SkillFileProblem{Kind: SkillFileProblemKind(problem.Problem), Line: problem.Line}
}

// ListUnmanagedSkills 列出各作用域(每个已添加的 Agent 的全局,以及它在每个已登记项目里)的全部技能目录里
// 不是库装的技能文件夹,和 ListSkillScopes 列出的库装的技能放在一起,就是 Agent 在这个作用域能看到的技能。
// 技能文件夹是技能目录下直接的子文件夹,或指向文件夹的符号链接,根上有 SKILL.md;以点开头的(如 Codex 的 .system)不算。
// 文件夹的实际路径上有安装记录的(哪个 Agent 记的都算,按记录所在的技能目录判断,不跟随符号链接)是库装的,不列出;
// 库装出去后被改过的也不在这里,巡检报为 modified。同一个作用域里经符号链接指到另一个目录里同一个文件夹的只算一处:
// 那个文件夹是库装的时链接不列出,都不是时只列真实的文件夹。这个作用域已经在一个技能目录里认得这个技能时,另一个技能目录里
// 和它的当前版本内容一致的那一份不列出,也不记安装;库里的技能更新后对不上了,它就是未纳管技能(SkillID 为那个技能、VersionID 为旧版)。
// 插件里的技能、企业托管目录、Codex 的 ADMIN / SYSTEM 技能和没有登记的项目都不在范围内。
// 不改磁盘。Agent 目录不存在、项目不在这台机器上时那个作用域没有结果;几个 Agent 读同一个目录时,
// 每个 Agent 各列一条。结果按 Agent、作用域(全局在前,项目按 ID)、Name、技能目录在这个 Agent 目录列表里的先后排序。
// 按内容认出它是库里的哪个技能、哪一版(SkillID、VersionID),或是库里哪个技能在来源里现在的那一版(SourceLatestOf);
// 为此只读 SKILL.md 和库里某个版本相同、或技能名和有新版本的库里技能相同的那几个文件夹的全部内容。不联网。
// 认出是库里的技能的,除 UnmanagedSkill.SkillID 列出的几种外,先自动纳入:记为这个技能直接装在这个作用域的那个技能目录里
// (和 FetchUnmanaged 补记的一样),结果里不再有它,之后巡检、更新、重新安装、回滚、卸载与移除技能都包括这一处。
// 一个作用域里有几个文件夹和同一个技能一致时只记一处:技能目录在 Agent 的目录列表里靠前的优先,同一个目录里文件夹名和 SKILL.md 里的 name
// 相同的优先,其次按文件夹名取第一个;已经认得到这个技能的 Agent 不再记(几个 Agent 读同一个目录时只记一处,其余本来就算已装)。
// SKILL.md 超过列出时的读取上限(1 MiB)的只认出、不自动纳入:列出时读不到它的 name,查不了它是不是记在 skills-lock.json 里。
// 存着 WSL 发行版([Library.WSLDistro])、全局目录却还在 Windows 本机时(启动时发行版打不开,或还没调用 [Library.SetWSLDistro] 换过去)
// 只列出、不自动纳入:全局安装不分主目录,这时记下的安装换到发行版后都会是缺失。换过去之后照常纳入。
func (l *Library) ListUnmanagedSkills(ctx context.Context) (skills []UnmanagedSkill, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	found, err := l.adoptedUnmanaged(ctx, nil)
	if err != nil {
		return nil, err
	}
	skills = []UnmanagedSkill{}
	for _, entry := range found {
		skills = append(skills, entry.UnmanagedSkill)
	}
	return skills, nil
}

type unmanagedEntry struct {
	UnmanagedSkill
	found scopeFolder
}

func (l *Library) recognizedUnmanaged(ctx context.Context, only map[Scope]bool) ([]unmanagedEntry, error) {
	entries, err := l.unmanagedSkills(ctx, only)
	if err != nil {
		return nil, err
	}
	return entries, l.recognizeUnmanaged(ctx, entries)
}

func (l *Library) adoptedUnmanaged(ctx context.Context, only map[Scope]bool) ([]unmanagedEntry, error) {
	found, err := l.recognizedUnmanaged(ctx, only)
	if err != nil {
		return nil, err
	}
	adopted, err := l.adoptMatching(ctx, found)
	if err != nil {
		return nil, err
	}
	if adopted {
		if found, err = l.recognizedUnmanaged(ctx, only); err != nil {
			return nil, err
		}
	}
	return l.withoutCopies(ctx, found)
}

func (l *Library) withoutCopies(ctx context.Context, entries []unmanagedEntry) ([]unmanagedEntry, error) {
	folders := make([]scopeFolder, len(entries))
	for i, entry := range entries {
		folders[i] = entry.found
	}
	copies, err := l.currentCopies(ctx, folders)
	if err != nil {
		return nil, err
	}
	return withoutMarked(entries, copies), nil
}

func (l *Library) adoptMatching(ctx context.Context, entries []unmanagedEntry) (bool, error) {
	chosenDistro, err := l.store.WSLDistro(ctx)
	if err != nil || chosenDistro != l.distro {
		return false, err
	}
	var chosen []unmanagedEntry
	for _, entry := range entries {
		if entry.SkillID == 0 || entry.InRepository || entry.InProjectLock || entry.LinkOutside || entry.found.SkillMD == nil {
			continue
		}
		other := slices.IndexFunc(chosen, func(c unmanagedEntry) bool {
			return c.SkillID == entry.SkillID && c.Scope == entry.Scope && c.found.target.relative == entry.found.target.relative
		})
		switch {
		case other < 0:
			chosen = append(chosen, entry)
		case entry.Name == skillName(entry.found.Folder) && chosen[other].Name != skillName(chosen[other].found.Folder):
			chosen[other] = entry
		}
	}
	if len(chosen) == 0 {
		return false, nil
	}
	slices.SortStableFunc(chosen, func(a, b unmanagedEntry) int {
		return cmp.Compare(slices.Index(a.found.target.paths, a.found.target.relative), slices.Index(b.found.target.paths, b.found.target.relative))
	})
	adopted := false
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		now := clockNow()
		for _, entry := range chosen {
			recorded, err := tx.RecordFetchedSkillScope(ctx, store.NewSkillScope{
				SkillID: int64(entry.SkillID), Scope: entry.found.target.Scope, Directory: entry.found.target.relative, Folder: entry.Name, CreatedAt: now,
			}, entry.found.target.sameDirectory)
			if err != nil {
				return err
			}
			adopted = adopted || recorded
		}
		return nil
	})
	return adopted, err
}

func (l *Library) unmanagedSkills(ctx context.Context, only map[Scope]bool) ([]unmanagedEntry, error) {
	folders, err := l.scopeFolders(ctx, only)
	if err != nil {
		return nil, err
	}
	committed := l.committedFolders()
	outside, err := l.linksOutside(ctx, folders)
	if err != nil {
		return nil, err
	}
	records := l.sourceRecords()
	var entries []unmanagedEntry
	for _, found := range folders {
		if found.skillID != 0 {
			continue
		}
		target := found.target
		entry := unmanagedEntry{UnmanagedSkill: UnmanagedSkill{Scope: target.scope(), Directory: l.displayDirectory(target), Name: found.Name, Folder: found.Path, LinkTarget: found.Target}, found: found}
		entry.LinkOutside = outside[found.Target]
		entry.AgentSettings = agentSettingsOf(found.SkillMD, found.CodexMetadata)
		if found.SkillMD != nil {
			if candidate, err := discovery.ParseSkillMD(found.SkillMD, found.Name); err == nil {
				entry.Description = candidate.Description
			} else {
				entry.Invalid = skillFileProblem(err)
			}
		}
		if record, ok := records.find(target, skillName(found.Folder), found.Name); ok {
			entry.SourceInput, entry.SourceRef = record.input, record.ref
			entry.InProjectLock = target.ProjectID != nil
		}
		if entry.InRepository, err = committed.has(ctx, target, found.Name); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b unmanagedEntry) int {
		return cmp.Or(cmp.Compare(a.Scope.Agent, b.Scope.Agent), cmp.Compare(a.Scope.ProjectID, b.Scope.ProjectID), cmp.Compare(a.Name, b.Name),
			cmp.Compare(slices.Index(a.found.target.paths, a.found.target.relative), slices.Index(b.found.target.paths, b.found.target.relative)))
	})
	if len(entries) == 0 {
		return entries, nil
	}
	everywhere := folders
	if only != nil {
		if everywhere, err = l.readableScopeFolders(ctx); err != nil {
			return nil, err
		}
	}
	same := sameFolders{folders: everywhere, committed: committed, records: records}
	for i, entry := range entries {
		for _, other := range same.of(ctx, entry.found.target, entry.found.canonical) {
			entries[i].SameFolder = append(entries[i].SameFolder, other.target.scope())
		}
	}
	return entries, nil
}

type repository struct {
	projectID ProjectID
	relative  string
}

type committedFolders struct {
	environ func() []string
	found   map[repository]map[string]bool
}

func (l *Library) committedFolders() committedFolders {
	return committedFolders{environ: l.environ, found: map[repository]map[string]bool{}}
}

func (c committedFolders) has(ctx context.Context, target resolvedScope, name string) (bool, error) {
	if target.ProjectID == nil {
		return false, nil
	}
	key := repository{projectID: ProjectID(*target.ProjectID), relative: target.relative}
	if _, ok := c.found[key]; !ok {
		committed, err := agentdir.Committed(process.WithEnv(ctx, c.environ), target.root, target.relative)
		if err != nil {
			return false, err
		}
		c.found[key] = committed
	}
	return c.found[key][name], nil
}

type sameFolders struct {
	folders   []scopeFolder
	committed committedFolders
	records   *sourceRecords
}

func (s sameFolders) of(ctx context.Context, target resolvedScope, canonical string) []scopeFolder {
	var same []scopeFolder
	for _, other := range s.folders {
		if other.skillID != 0 || other.canonical != canonical || other.target.scope() == target.scope() || target.sharesFolderWith(other.target) {
			continue
		}
		if inRepository, err := s.committed.has(ctx, other.target, other.Name); err != nil || inRepository {
			continue
		}
		if _, inLock := s.records.find(other.target, skillName(other.Folder), other.Name); inLock && other.target.ProjectID != nil {
			continue
		}
		same = append(same, other)
	}
	return same
}

func (l *Library) recognizeUnmanaged(ctx context.Context, entries []unmanagedEntry) error {
	latests, err := l.store.SourceLatests(ctx)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, latest := range latests {
		names[latest.Name] = true
	}
	manifests, err := l.store.ManifestFingerprints(ctx)
	if err != nil {
		return err
	}
	sourceless, err := l.store.SourcelessSkills(ctx)
	if err != nil {
		return err
	}
	var paths []string
	var indexes []int
	for i, entry := range entries {
		if !validFolderName(entry.Name) {
			continue
		}
		if entry.found.SkillMD != nil && !manifests[skillfile.Fingerprint(entry.found.SkillMD)] && !names[skillName(entry.found.Folder)] {
			continue
		}
		paths = append(paths, entry.Folder)
		indexes = append(indexes, i)
	}
	return agentdir.ReadFolders(ctx, paths, maxFetchFiles, maxFetchSize, func(index int, state agentdir.FolderState) error {
		if !state.Exists || state.Err != nil {
			return nil
		}
		entry := &entries[indexes[index]]
		found, err := recognize(ctx, l.store, diskOf(state), latests)
		if err != nil {
			return err
		}
		entry.recognized(found, sourceless)
		return nil
	})
}

func (u *UnmanagedSkill) recognized(found recognition, sourceless map[int64]string) {
	u.SkillID, u.VersionID, u.SourceLatestOf = found.skillID, found.versionID, found.sourceLatestOf
	if _, ok := sourceless[int64(found.skillID)]; found.skillID != 0 && !ok {
		u.SourceInput, u.SourceRef = "", ""
	}
}

func (l *Library) linksOutside(ctx context.Context, folders []scopeFolder) (map[string]bool, error) {
	var targets []string
	for _, found := range folders {
		if found.skillID == 0 && found.Target != "" && !slices.Contains(targets, found.Target) {
			targets = append(targets, found.Target)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	directories, err := l.agentDirectories(ctx)
	if err != nil {
		return nil, err
	}
	inside, err := agentdir.Within(ctx, directories, targets)
	if err != nil {
		return nil, err
	}
	outside := map[string]bool{}
	for i, target := range targets {
		outside[target] = !inside[i]
	}
	return outside, nil
}

func (l *Library) agentDirectories(ctx context.Context) ([]string, error) {
	agents, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := l.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var directories []string
	for _, agent := range agents {
		for _, path := range agent.GlobalPaths {
			directories = append(directories, l.home.Join(path))
		}
		for _, project := range projects {
			for _, path := range agent.ProjectPaths {
				directories = append(directories, agentdir.Join(project.Path, path))
			}
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(directories))), nil
}

func (l *Library) displayDirectory(target resolvedScope) string {
	return l.shownDirectory(target.ProjectID != nil, target.relative)
}

func (l *Library) shownDirectory(project bool, relative string) string {
	if !project {
		return l.home.Join(relative)
	}
	return relative
}

func (l *Library) scopeIn(operation string, target resolvedScope, directory string) (resolvedScope, error) {
	if directory == "" {
		return target, nil
	}
	for _, path := range target.paths {
		if directory == path || target.ProjectID == nil && agentdir.SameEntry(directory, l.home.Join(path)) {
			return target.in(path), nil
		}
	}
	return resolvedScope{}, argumentError(operation, "directory", "the agent does not read skills from this directory")
}

// SameNamePairKind 是两个技能文件夹同名的情形。值以后可能增加,调用方遇到不认识的值按「只标出同名」处理。
type SameNamePairKind string

const (
	// SameNameGlobalAndProject 是同一个 Agent 的全局和一个已登记项目里同名:后果按 Agent 的 [Agent].SameName 说明。
	SameNameGlobalAndProject SameNamePairKind = "global_and_project"
	// SameNameTwoDirectories 是同一个作用域的两个技能目录里同名、又不是同一个技能:不知道哪个生效,只标出同名。
	SameNameTwoDirectories SameNamePairKind = "two_directories"
)

// SameNameFolder 是同名技能中的一个技能文件夹。
type SameNameFolder struct {
	Scope Scope `json:"scope"`
	// Directory 是文件夹所在的技能目录,写法同 [SkillScope].Directory。
	Directory string `json:"directory"`
	// Folder 是技能文件夹的路径,与 [SkillScope].Folder、[UnmanagedSkill].Folder 相同,界面按它对上列表里的那一项。
	Folder string `json:"folder"`
	// SkillID 在这个文件夹上有安装记录(库装的)时是那个技能的 ID;未纳管技能为 0。
	SkillID SkillID `json:"skill_id,omitzero"`
}

// SameNameSkill 是一对同名的技能文件夹,两处都要标出来。
type SameNameSkill struct {
	Kind SameNamePairKind `json:"kind"`
	// Name 是两处共同的技能名:SKILL.md 里的 name,读不出时用文件夹名。两处的文件夹名可以不同。
	Name string `json:"name"`
	// First 在 SameNameGlobalAndProject 时是全局的那个,在 SameNameTwoDirectories 时是技能目录在 Agent 目录列表里靠前的那个;
	// Second 是另一个。
	First  SameNameFolder `json:"first"`
	Second SameNameFolder `json:"second"`
}

// ListSameNameSkills 列出同名的技能文件夹,不论是不是库装的:库装的([Library.ListSkillScopes])和未纳管的([Library.ListUnmanagedSkills])
// 一起比,也就是 Agent 实际能看到的技能。有两种(见 SameNamePairKind):每个已添加的 Agent 的全局和各已登记项目里同名的;
// 同一个作用域两个技能目录里同名、又不是同一个技能的(两处是同一个技能的安装记录,或一处是这个技能的安装、另一处和它的当前版本内容一致,
// 都不算;经符号链接指到同一个文件夹的只算一处)。和另一个技能目录里装着的技能的当前版本内容一致的那一份,和 ListUnmanagedSkills 一样不算。
// 同名按 SKILL.md 的 frontmatter 里的 name 判断(Agent 按它认技能),frontmatter 读不出或没有合法的 name 时用文件夹名;
// 技能文件夹的认法与 ListUnmanagedSkills 相同,安装记录在、文件夹不在了的不算。
// 每一对同名的文件夹各一条:全局里的一个技能和几个项目里的同名时每个项目各一条,同一处有几个文件夹同名时两两各一条。
// 几个 Agent 读同一个目录时每个 Agent 各列一条。只读:不改库、不改磁盘。Agent 目录不存在、项目不在这台机器上时那里没有结果。
// 结果按 Kind、Agent、项目 ID、Name、First 的文件夹、Second 的文件夹排序。
func (l *Library) ListSameNameSkills(ctx context.Context) (skills []SameNameSkill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	folders, err := l.scopeFolders(ctx, nil)
	if err != nil {
		return nil, err
	}
	copies, err := l.currentCopies(ctx, folders)
	if err != nil {
		return nil, err
	}
	folders = withoutMarked(folders, copies)
	type named struct {
		scope Scope
		name  string
	}
	byScope := map[named][]scopeFolder{}
	var order []named
	for _, found := range folders {
		key := named{found.target.scope(), skillName(found.Folder)}
		if byScope[key] == nil {
			order = append(order, key)
		}
		byScope[key] = append(byScope[key], found)
	}
	sameNameFolder := func(found scopeFolder) SameNameFolder {
		return SameNameFolder{Scope: found.target.scope(), Directory: l.displayDirectory(found.target), Folder: found.Path, SkillID: found.skillID}
	}
	skills = []SameNameSkill{}
	for _, key := range order {
		group := byScope[key]
		for i, first := range group {
			for _, second := range group[i+1:] {
				if first.target.relative == second.target.relative || first.skillID != 0 && first.skillID == second.skillID {
					continue
				}
				skills = append(skills, SameNameSkill{Kind: SameNameTwoDirectories, Name: key.name, First: sameNameFolder(first), Second: sameNameFolder(second)})
			}
		}
		if key.scope.Kind != ScopeProject {
			continue
		}
		for _, global := range byScope[named{GlobalScope(key.scope.Agent), key.name}] {
			for _, project := range group {
				skills = append(skills, SameNameSkill{Kind: SameNameGlobalAndProject, Name: key.name, First: sameNameFolder(global), Second: sameNameFolder(project)})
			}
		}
	}
	slices.SortFunc(skills, func(a, b SameNameSkill) int {
		return cmp.Or(
			cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Second.Scope.Agent, b.Second.Scope.Agent), cmp.Compare(a.Second.Scope.ProjectID, b.Second.Scope.ProjectID),
			cmp.Compare(a.Name, b.Name), cmp.Compare(a.First.Folder, b.First.Folder), cmp.Compare(a.Second.Folder, b.Second.Folder),
		)
	})
	return skills, nil
}

func (l *Library) currentCopies(ctx context.Context, folders []scopeFolder) ([]bool, error) {
	copies := make([]bool, len(folders))
	installed, err := l.listSkillScopes(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	heads := map[SkillID]map[string]skillfile.Digest{}
	current := func(id SkillID) (map[string]skillfile.Digest, error) {
		if digests, ok := heads[id]; ok {
			return digests, nil
		}
		_, head, err := l.store.SkillHead(ctx, int64(id))
		if err != nil {
			return nil, err
		}
		heads[id], err = l.store.VersionDigests(ctx, head)
		return heads[id], err
	}
	var paths []string
	var indexes []int
	var candidates [][]map[string]skillfile.Digest
	for i, found := range folders {
		if found.skillID != 0 {
			continue
		}
		var versions []map[string]skillfile.Digest
		for _, skillScope := range installed {
			if skillScope.Scope != found.target.scope() || skillScope.Directory == l.displayDirectory(found.target) {
				continue
			}
			digests, err := current(skillScope.SkillID)
			if err != nil {
				return nil, err
			}
			if found.SkillMD == nil || digests["SKILL.md"].Fingerprint == skillfile.Fingerprint(found.SkillMD) {
				versions = append(versions, digests)
			}
		}
		if len(versions) > 0 {
			paths = append(paths, found.Path)
			indexes = append(indexes, i)
			candidates = append(candidates, versions)
		}
	}
	err = agentdir.ReadFolders(ctx, paths, maxFetchFiles, maxFetchSize, func(index int, state agentdir.FolderState) error {
		if state.Exists && state.Err == nil {
			copies[indexes[index]] = slices.ContainsFunc(candidates[index], diskOf(state).same)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return copies, nil
}

func withoutMarked[T any](items []T, marked []bool) []T {
	kept := make([]T, 0, len(items))
	for i, item := range items {
		if !marked[i] {
			kept = append(kept, item)
		}
	}
	return kept
}

func skillName(folder agentdir.Folder) string {
	candidate, _ := discovery.ParseSkillMD(folder.SkillMD, folder.Name)
	return cmp.Or(candidate.Name, folder.Name)
}

type scopeFolder struct {
	agentdir.Folder
	target    resolvedScope
	canonical string
	skillID   SkillID
	twins     []string
}

func (l *Library) scopeFolders(ctx context.Context, only map[Scope]bool) ([]scopeFolder, error) {
	return l.listScopeFolders(ctx, only, false)
}

func (l *Library) readableScopeFolders(ctx context.Context) ([]scopeFolder, error) {
	return l.listScopeFolders(ctx, nil, true)
}

func (l *Library) listScopeFolders(ctx context.Context, only map[Scope]bool, skipUnreadable bool) ([]scopeFolder, error) {
	agents, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := l.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := l.store.ListSkillScopes(ctx, 0)
	if err != nil {
		return nil, err
	}
	type folder struct {
		projectID int64
		name      string
	}
	type claim struct {
		directory string
		skillID   SkillID
	}
	claimed := map[folder][]claim{}
	for _, row := range installed {
		key := folder{name: row.Name}
		if row.Scope.ProjectID != nil {
			key.projectID = *row.Scope.ProjectID
		}
		claimed[key] = append(claimed[key], claim{row.Directory, SkillID(row.SkillID)})
	}
	type rootCheck struct {
		missing *Error
		err     error
	}
	roots := map[string]rootCheck{}
	var listings []resolvedScope
	var directories []string
	seen := map[string]bool{}
	for _, agent := range agents {
		if !agent.Enabled {
			continue
		}
		rows := []store.Scope{{Agent: agent.Code}}
		for _, project := range projects {
			rows = append(rows, store.Scope{Agent: agent.Code, ProjectID: new(project.ID)})
		}
		for _, row := range rows {
			if only != nil && !only[scopeFrom(row)] {
				continue
			}
			target, err := l.resolveRow(ctx, row)
			if err != nil && skipUnreadable {
				continue
			}
			if err != nil {
				return nil, err
			}
			check, checked := roots[target.root]
			if !checked {
				check.missing, check.err = target.rootMissing("list skill folders")
				roots[target.root] = check
			}
			missing, err := check.missing, check.err
			if err != nil && !skipUnreadable {
				return nil, err
			}
			if err != nil || missing != nil {
				continue
			}
			for _, path := range target.paths {
				each := target.in(path)
				if !seen[each.directory] {
					seen[each.directory] = true
					directories = append(directories, each.directory)
				}
				listings = append(listings, each)
			}
		}
	}
	resolved, err := agentdir.CanonicalAll(ctx, directories)
	if err != nil {
		return nil, err
	}
	canonical := map[string]string{}
	for i, directory := range resolved {
		canonical[directories[i]] = directory
	}
	type listed struct {
		folders []agentdir.Folder
		err     error
	}
	lists := map[string]listed{}
	var folders []scopeFolder
	for _, target := range listings {
		directory := canonical[target.directory]
		list, ok := lists[directory]
		if !ok {
			list.folders, list.err = agentdir.List(target.directory)
			lists[directory] = list
		}
		if list.err != nil && skipUnreadable {
			continue
		}
		if list.err != nil {
			return nil, list.err
		}
		key := folder{}
		if target.ProjectID != nil {
			key.projectID = *target.ProjectID
		}
		for _, found := range list.folders {
			own := found
			own.Path = agentdir.FolderPath(target.directory, found.Name)
			key.name = found.Name
			var skillID SkillID
			for _, claim := range claimed[key] {
				if target.reads(claim.directory) {
					skillID = claim.skillID
				}
			}
			folders = append(folders, scopeFolder{
				Folder: own, target: target, canonical: cmp.Or(found.Target, agentdir.FolderPath(directory, found.Name)),
				skillID: skillID,
			})
		}
	}
	return withoutLinkedTwins(folders), nil
}

func withoutLinkedTwins(folders []scopeFolder) []scopeFolder {
	type twin struct {
		scope     Scope
		canonical string
	}
	kept := map[twin]int{}
	var result []scopeFolder
	for _, found := range folders {
		key := twin{found.target.scope(), found.canonical}
		at, ok := kept[key]
		if ok && result[at].target.relative == found.target.relative {
			result = append(result, found)
			continue
		}
		if !ok {
			kept[key] = len(result)
			result = append(result, found)
			continue
		}
		if other := result[at]; other.skillID == 0 && (found.skillID != 0 || other.Target != "" && found.Target == "") {
			found.twins = append(other.twins, other.Path)
			result[at] = found
		} else {
			result[at].twins = append(result[at].twins, found.Path)
		}
	}
	return result
}

func validFolderName(name string) bool {
	return discovery.ValidName(name) && !strings.HasPrefix(name, ".")
}

func readSkillFolder(ctx context.Context, operation, path, name string) (diskFiles, discovery.Candidate, error) {
	state, err := agentdir.ReadFolder(ctx, path, maxFetchFiles, maxFetchSize)
	if err != nil {
		return diskFiles{}, discovery.Candidate{}, err
	}
	return parseSkillFolder(operation, path, name, state)
}

func parseSkillFolder(operation, path, name string, state agentdir.FolderState) (diskFiles, discovery.Candidate, error) {
	switch err := state.Err; {
	case errors.Is(err, agentdir.ErrIrregularFile):
		return diskFiles{}, discovery.Candidate{}, preconditionError(operation, "the folder has entries that are not regular files", ErrorDetails{Path: path})
	case errors.Is(err, agentdir.ErrTooLarge):
		return diskFiles{}, discovery.Candidate{}, &Error{Code: SourceInvalid, Message: operation + ": " + err.Error(), Details: ErrorDetails{Path: path}, Err: err}
	case err != nil:
		return diskFiles{}, discovery.Candidate{}, err
	}
	index := slices.IndexFunc(state.Files, func(file skillfile.File) bool { return file.Path == "SKILL.md" })
	if index < 0 {
		return diskFiles{}, discovery.Candidate{}, &Error{Code: NotFound, Message: operation + ": the folder has no SKILL.md", Details: ErrorDetails{Path: path}}
	}
	parsed, err := discovery.ParseSkillMD(state.Files[index].Content, name)
	if err != nil {
		return diskFiles{}, discovery.Candidate{}, &Error{Code: SourceInvalid, Message: operation + ": SKILL.md: " + err.Error(), Details: ErrorDetails{Path: path}, Err: err}
	}
	return diskOf(state), parsed, nil
}

// FindUnmanagedSourcesRequest 指定要去 skills.sh 找来源的未纳管技能。
type FindUnmanagedSourcesRequest struct {
	// Folders 是 ListUnmanagedSkills 列出的条目,按作用域、技能目录与文件夹名指定,重复的只算一次。
	// 来源记录已经认出来源(UnmanagedSkill.SourceInput 不为空)的、按内容认出是库里哪个技能的(SkillID 或 SourceLatestOf 不为 0),
	// 和已经不是未纳管技能、不在了的一样,不找也不报错。
	Folders []FolderInScope `json:"folders"`
}

// FoundUnmanagedSource 是在 skills.sh 上给一个未纳管技能找到的来源。
type FoundUnmanagedSource struct {
	Scope     Scope  `json:"scope"`
	Directory string `json:"directory"` // UnmanagedSkill.Directory,即文件夹所在的技能目录
	Name      string `json:"name"`      // UnmanagedSkill.Name,即技能文件夹名
	// SourceInput 是找到的来源,和来源记录认出的 UnmanagedSkill.SourceInput 一样用:界面写「来自 …」,入库确认里预填给
	// FetchUnmanagedRequest.SourceInput 与 UnmanagedFetchItem.SourceInput,整批确认按它分组。格式同 PreviewFetchRequest.Input:
	// GitHub 仓库为 owner/repo,well-known 站点为 https://<站点>;不带分支,SourceRef 留空。
	SourceInput string `json:"source_input"`
}

// FindUnmanagedSources 给来源记录认不出来源的未纳管技能去 skills.sh 找来源:按 SKILL.md 里的 name 搜索,只看名字和它相同、
// 能转成入库预览输入的结果(规则同 SearchSkills),读出来源里这个技能的文件,和磁盘上的内容比较相似度,
// 得分不低于 60% 的几个里只取优先级最高的一个:库里已经在跟的来源(有技能的集合)在前,几个都在跟时按集合的先后(ListCollections 的顺序);
// 再按得分从高到低,一样高时取 skills.sh 排在前面的。相似度的算法和推测来源里改名的技能相同:
// 同路径的文件按非空行的重合比例算分(SKILL.md 不算 name 那一行),相加后除以两边文件数中较多的那个。
// 已经认出是库里哪个技能的(UnmanagedSkill.SkillID 或 SourceLatestOf 不为 0)不找:它跟着库里那个技能的来源。
// 用找到的来源入库(FetchUnmanaged、FetchUnmanagedSkills)时,内容和来源里的一致就是来源里的那一版,不一致时和用户自己给的来源一样,
// 版本标为 SkillVersion.MaybeEdited。
// 它要联网,ListUnmanagedSkills 与 PlanFetchUnmanaged 不等它:界面列出之后在后台调用,结果到了再补上。
// 搜索 skills.sh 最多同时 4 个;都搜完后再读来源,最多同时读 2 个来源,一个来源里要的技能都读完才读下一个;
// 同一个来源这一次只读一次,几个技能共用。联网期间不持库锁。
// 搜不到、没有达到阈值的、skills.sh 或来源连不上、文件夹读不成的,都当作认不出来源:不在结果里,也不报错。
// 只读:不改库、不改磁盘。结果按请求顺序,只列出找到来源的。
// 错误:作用域不合法返回 InvalidArgument(Details.Field 指向对应字段),文件夹名不合法为 name;Agent、项目不存在返回 NotFound。
func (l *Library) FindUnmanagedSources(ctx context.Context, req FindUnmanagedSourcesRequest) (found []FoundUnmanagedSource, err error) {
	defer func() { err = typedError(err) }()
	folders, err := l.sourcelessFolders(ctx, req.Folders)
	if err != nil {
		return nil, err
	}
	searched := map[string][]string{}
	for _, folder := range folders {
		searched[folder.name] = nil
	}
	limit := make(parallelLimit, maxSourceSearches)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name := range maps.Clone(searched) {
		wg.Go(func() {
			limit.do(ctx, func() {
				results, err := market.Search(ctx, l.client, name)
				if err != nil {
					return
				}
				var inputs []string
				for _, result := range results {
					if _, input, ok := catalog.SearchInputs(result.Source, result.Name); ok && result.Name == name && !slices.Contains(inputs, input) {
						inputs = append(inputs, input)
					}
				}
				mu.Lock()
				searched[name] = inputs
				mu.Unlock()
			})
		})
	}
	wg.Wait()
	wanted := map[string][]string{}
	for name, inputs := range searched {
		for _, input := range inputs {
			wanted[input] = append(wanted[input], name)
		}
	}
	type sourceSkill struct{ input, name string }
	fetched := map[sourceSkill][]skillfile.File{}
	opened := map[string]store.Source{}
	reads := make(parallelLimit, maxSourceReads)
	for input, names := range wanted {
		wg.Go(func() {
			reads.do(ctx, func() {
				source, err := l.openSource(ctx, input, "")
				if err != nil {
					return
				}
				mu.Lock()
				opened[input] = store.Source{Type: source.target.Type, Locator: source.target.Locator}
				mu.Unlock()
				for _, name := range names {
					skills, err := source.snapshot.Fetch(ctx, []string{name})
					if err != nil || len(skills) != 1 {
						continue
					}
					mu.Lock()
					fetched[sourceSkill{input, name}] = skills[0].Files
					mu.Unlock()
				}
			})
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	followed, err := l.followedRanks(ctx, opened)
	if err != nil {
		return nil, err
	}
	found = []FoundUnmanagedSource{}
	for _, folder := range folders {
		type candidate struct {
			input string
			rank  int
			score float64
		}
		var best *candidate
		for _, input := range searched[folder.name] {
			files, ok := fetched[sourceSkill{input, folder.name}]
			if !ok {
				continue
			}
			each := candidate{input: input, rank: followed[input], score: skillfile.Similarity(folder.files, files)}
			if each.score >= renameSimilarity && (best == nil || each.rank < best.rank || each.rank == best.rank && each.score > best.score) {
				best = &each
			}
		}
		if best != nil {
			found = append(found, FoundUnmanagedSource{Scope: folder.Scope, Directory: folder.Directory, Name: folder.Name, SourceInput: best.input})
		}
	}
	return found, nil
}

func (l *Library) followedRanks(ctx context.Context, opened map[string]store.Source) (map[string]int, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	order, err := l.store.SourceOrder(ctx)
	if err != nil {
		return nil, err
	}
	ranks := make(map[string]int, len(opened))
	for input, source := range opened {
		id, err := l.store.FollowedSource(ctx, source.Type, source.Locator)
		if err != nil {
			return nil, err
		}
		ranks[input] = len(order)
		if index := slices.Index(order, id); id != 0 && index >= 0 {
			ranks[input] = index
		}
	}
	return ranks, nil
}

const (
	maxSourceSearches = 4
	maxSourceReads    = 2
)

type parallelLimit chan struct{}

func (p parallelLimit) do(ctx context.Context, work func()) {
	select {
	case p <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-p }()
	work()
}

type sourcelessFolder struct {
	FolderInScope
	name  string
	files []skillfile.File
}

func (l *Library) sourcelessFolders(ctx context.Context, requested []FolderInScope) ([]sourcelessFolder, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	const operation = "find unmanaged sources"
	type folderKey struct {
		scope     Scope
		directory string
		name      string
	}
	wanted := map[Scope]bool{}
	keys := make([]folderKey, len(requested))
	for i, folder := range requested {
		if !validFolderName(folder.Name) {
			return nil, argumentError(operation, "name", "invalid skill folder name")
		}
		target, err := l.resolveScope(ctx, operation, folder.Scope)
		if err != nil {
			return nil, err
		}
		if target, err = l.scopeIn(operation, target, folder.Directory); err != nil {
			return nil, err
		}
		wanted[folder.Scope] = true
		keys[i] = folderKey{folder.Scope, target.relative, folder.Name}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	entries, err := l.recognizedUnmanaged(ctx, wanted)
	if err != nil {
		return nil, err
	}
	listed := map[folderKey]unmanagedEntry{}
	for _, entry := range entries {
		listed[folderKey{entry.Scope, entry.found.target.relative, entry.Name}] = entry
	}
	var found []unmanagedEntry
	var paths []string
	for _, key := range keys {
		entry, ok := listed[key]
		if !ok || entry.SourceInput != "" || entry.SkillID != 0 || entry.SourceLatestOf != 0 {
			continue
		}
		delete(listed, key)
		found = append(found, entry)
		paths = append(paths, entry.Folder)
	}
	read := make([]*sourcelessFolder, len(found))
	err = agentdir.ReadFolders(ctx, paths, maxFetchFiles, maxFetchSize, func(i int, state agentdir.FolderState) error {
		if disk, parsed, err := parseSkillFolder(operation, state.Path, found[i].Name, state); err == nil {
			folder := FolderInScope{Scope: found[i].Scope, Directory: found[i].Directory, Name: found[i].Name}
			read[i] = &sourcelessFolder{FolderInScope: folder, name: parsed.Name, files: disk.files}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var folders []sourcelessFolder
	for _, folder := range read {
		if folder != nil {
			folders = append(folders, *folder)
		}
	}
	return folders, nil
}
