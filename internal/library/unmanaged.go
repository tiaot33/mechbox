package library

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/market"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/skilllock"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// UnmanagedSkill 是作用域的技能目录里一个不是库装的技能文件夹:这个作用域里没有安装记录用它。它可能是命令行工具
// (如 npx skills)装的、手动拷进去的、用户自己写的,或者提交在项目仓库里的。库只列出它,不改动、不删除。
type UnmanagedSkill struct {
	Scope Scope `json:"scope"`
	// Name 是文件夹名。
	Name string `json:"name"`
	// Description 取自 SKILL.md 的 frontmatter;frontmatter 读不出时为空。
	Description string `json:"description,omitempty"`
	// Folder 是技能文件夹在作用域目录里的路径;文件夹是符号链接时就是链接本身,「在 Finder 中显示」用它。
	Folder string `json:"folder"`
	// LinkTarget 在文件夹是符号链接时给出它最终指向的目录(已解析各级符号链接),否则为空。
	// 例如 npx skills 把真实文件夹放在 ~/.agents/skills/<name>,再从其他 Agent 的目录用符号链接指过去。
	LinkTarget string `json:"link_target,omitempty"`
	// LinkOutside 表示 LinkTarget 不在任何 Agent 的技能目录里(各 Agent 的全局目录与在各已登记项目里的技能目录),
	// 例如技能作者把它链到自己的工作目录 ~/code/my-skills/foo,在那里改了 Agent 马上就能用上。入库后库第一次写这一处时
	// 会把链接换成独立的一份,在原来的目录里改就不再影响这里;所以和在项目仓库里的一样默认不入库,界面写明它指向哪里。
	LinkOutside bool `json:"link_outside,omitzero"`
	// InRepository 表示文件夹已提交在项目的 git 仓库里(在 HEAD 里),界面标明「在项目仓库里」。只出现在项目作用域;
	// 项目不是 git 仓库、还没有提交或本机没有 git 时为 false。
	InRepository bool `json:"in_repository,omitzero"`
	// SourceInput 是从 npx skills 留下的来源记录里认出的来源,格式同 PreviewFetchRequest.Input;入库确认里预填给
	// FetchUnmanagedRequest.SourceInput,界面写「来自 …」。认不出时为空,用户可以自己给一个来源链接。
	// 全局作用域读 ~/.agents/.skill-lock.json(设了 XDG_STATE_HOME 时为 $XDG_STATE_HOME/skills/.skill-lock.json,只认 version 3 及以上),
	// 项目作用域读项目根目录的 skills-lock.json;记录按技能名对上文件夹:先比 SKILL.md 里的 name,再比文件夹名。
	// 记录里的来源类型为 github、gitlab、git、well-known、download 与 local(项目里记录的相对路径按项目根解析)时认得出,其他的认不出。
	SourceInput string `json:"source_input,omitempty"`
	// SourceRef 是来源记录里记的分支或 tag,只对 Git 类来源(github、gitlab、git)有;预填给 FetchUnmanagedRequest.SourceRef。
	SourceRef string `json:"source_ref,omitempty"`
	// InProjectLock 表示项目根目录的 skills-lock.json 记了这个技能(认不认得出来源都算):技能是团队约定装的,文件夹本身不一定提交了,
	// 和 InRepository 一样归项目仓库管。只出现在项目作用域。
	InProjectLock bool `json:"in_project_lock,omitzero"`
}

// ListUnmanagedSkills 列出各作用域(每个 Agent 的全局,以及每个 Agent 在每个已登记项目里)的技能目录里
// 不是库装的技能文件夹,和 ListSkillScopes 列出的库装的技能放在一起,就是 Agent 在这个作用域能看到的技能。
// 技能文件夹是技能目录下直接的子文件夹,或指向文件夹的符号链接,根上有 SKILL.md;以点开头的(如 Codex 的 .system)不算。
// 这个作用域里有安装记录用它的(含共用目录的 Agent 装的,共用按 Agent 的目录设置判断,不跟随符号链接)是库装的,不列出;
// 库装出去后被改过的也不在这里,巡检报为 modified。
// 插件里的技能、企业托管目录、Codex 的 ADMIN / SYSTEM 技能和没有登记的项目都不在范围内。
// 只读:不改库、不改磁盘。Agent 目录不存在、项目不在这台机器上时那个作用域没有结果;几个 Agent 共用一个目录时,
// 每个 Agent 各列一条。结果按 Agent、作用域(全局在前,项目按 ID)、Name 排序。
func (l *Library) ListUnmanagedSkills(ctx context.Context) (skills []UnmanagedSkill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	found, err := l.unmanagedSkills(ctx, nil)
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

func (l *Library) unmanagedSkills(ctx context.Context, only map[Scope]bool) ([]unmanagedEntry, error) {
	folders, err := l.scopeFolders(ctx, only)
	if err != nil {
		return nil, err
	}
	type repository struct {
		projectID ProjectID
		relative  string
	}
	committed := map[repository]map[string]bool{}
	directories, err := l.agentDirectories(ctx)
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
		entry := unmanagedEntry{UnmanagedSkill: UnmanagedSkill{Scope: target.scope(), Name: found.Name, Folder: found.Path, LinkTarget: found.Target}, found: found}
		entry.LinkOutside = found.Target != "" && !slices.ContainsFunc(directories, func(directory string) bool { return agentdir.Within(found.Target, directory) })
		if candidate, err := discovery.ParseSkillMD(found.SkillMD, found.Name); err == nil {
			entry.Description = candidate.Description
		}
		if record, ok := records.find(target, skillName(found.Folder), found.Name); ok {
			entry.SourceInput, entry.SourceRef, _ = record.Input()
			entry.InProjectLock = target.projectID != nil
		}
		if target.projectID != nil {
			key := repository{projectID: *target.projectID, relative: target.relative}
			if _, ok := committed[key]; !ok {
				if committed[key], err = agentdir.Committed(ctx, target.root, target.relative); err != nil {
					return nil, err
				}
			}
			entry.InRepository = committed[key][found.Name]
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b unmanagedEntry) int {
		return cmp.Or(cmp.Compare(a.Scope.Agent, b.Scope.Agent), cmp.Compare(a.Scope.ProjectID, b.Scope.ProjectID), cmp.Compare(a.Name, b.Name))
	})
	return entries, nil
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
		rows := []scopeRow{{agent: AgentCode(agent.Code)}}
		for _, project := range projects {
			rows = append(rows, scopeRow{agent: AgentCode(agent.Code), projectID: new(ProjectID(project.ID))})
		}
		for _, row := range rows {
			target, err := l.resolveRow(ctx, row)
			if err != nil {
				return nil, err
			}
			directories = append(directories, target.directory)
		}
	}
	return directories, nil
}

// SameNameFolder 是同名技能在一个作用域里的技能文件夹。
type SameNameFolder struct {
	// Folder 是技能文件夹的路径,与 [SkillScope].Folder、[UnmanagedSkill].Folder 相同,界面按它对上列表里的那一项。
	Folder string `json:"folder"`
	// SkillID 在这个作用域里有安装记录用它(库装的)时是那个技能的 ID;未纳管技能为 0。
	SkillID SkillID `json:"skill_id,omitzero"`
}

// SameNameSkill 是同一个 Agent 的全局和一个已登记项目里同名的两个技能文件夹,两处都要标出来。
// 后果按 Agent 的 [Agent].SameName 说明。
type SameNameSkill struct {
	Agent     AgentCode `json:"agent"`
	ProjectID ProjectID `json:"project_id"`
	// Name 是两处共同的技能名:SKILL.md 里的 name,读不出时用文件夹名。两处的文件夹名可以不同。
	Name    string         `json:"name"`
	Global  SameNameFolder `json:"global"`
	Project SameNameFolder `json:"project"`
}

// ListSameNameSkills 列出每个 Agent 的全局和各已登记项目里同名的技能文件夹,不论是不是库装的:库装的
// ([Library.ListSkillScopes])和未纳管的([Library.ListUnmanagedSkills])一起比,也就是 Agent 在这两处实际能看到的技能。
// 同名按 SKILL.md 的 frontmatter 里的 name 判断(Agent 按它认技能),frontmatter 读不出或没有合法的 name 时用文件夹名;
// 技能文件夹的认法与 ListUnmanagedSkills 相同,安装记录在、文件夹不在了的不算。
// 每一对同名的全局文件夹和项目文件夹各一条:全局里的一个技能和几个项目里的同名时每个项目各一条,同一处有几个文件夹同名时两两各一条。
// 几个 Agent 共用目录时每个 Agent 各列一条。只读:不改库、不改磁盘。Agent 目录不存在、项目不在这台机器上时那里没有结果。
// 结果按 Agent、项目 ID、Name、全局文件夹、项目文件夹排序。
func (l *Library) ListSameNameSkills(ctx context.Context) (skills []SameNameSkill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	folders, err := l.scopeFolders(ctx, nil)
	if err != nil {
		return nil, err
	}
	type global struct {
		agent AgentCode
		name  string
	}
	globals := map[global][]SameNameFolder{}
	for _, found := range folders {
		if found.target.projectID == nil {
			key := global{found.target.agent, skillName(found.Folder)}
			globals[key] = append(globals[key], SameNameFolder{Folder: found.Path, SkillID: found.skillID})
		}
	}
	skills = []SameNameSkill{}
	for _, found := range folders {
		if found.target.projectID == nil {
			continue
		}
		name := skillName(found.Folder)
		for _, other := range globals[global{found.target.agent, name}] {
			skills = append(skills, SameNameSkill{
				Agent: found.target.agent, ProjectID: *found.target.projectID, Name: name,
				Global: other, Project: SameNameFolder{Folder: found.Path, SkillID: found.skillID},
			})
		}
	}
	slices.SortFunc(skills, func(a, b SameNameSkill) int {
		return cmp.Or(
			cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.ProjectID, b.ProjectID), cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.Global.Folder, b.Global.Folder), cmp.Compare(a.Project.Folder, b.Project.Folder),
		)
	})
	return skills, nil
}

func skillName(folder agentdir.Folder) string {
	candidate, _ := discovery.ParseSkillMD(folder.SkillMD, folder.Name)
	return cmp.Or(candidate.Name, folder.Name)
}

type scopeFolder struct {
	agentdir.Folder
	target  resolvedScope
	skillID SkillID
}

func (l *Library) scopeFolders(ctx context.Context, only map[Scope]bool) ([]scopeFolder, error) {
	agents, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := l.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := l.listSkillScopes(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	type folder struct {
		scope Scope
		name  string
	}
	claimed := map[folder]SkillID{}
	for _, skillScope := range installed {
		claimed[folder{skillScope.Scope, filepath.Base(skillScope.Folder)}] = skillScope.SkillID
	}
	var folders []scopeFolder
	for _, agent := range agents {
		rows := []scopeRow{{agent: AgentCode(agent.Code)}}
		for _, project := range projects {
			rows = append(rows, scopeRow{agent: AgentCode(agent.Code), projectID: new(ProjectID(project.ID))})
		}
		for _, row := range rows {
			if only != nil && !only[row.scope()] {
				continue
			}
			target, err := l.resolveRow(ctx, row)
			if err != nil {
				return nil, err
			}
			if missing, err := target.rootMissing("list skill folders"); err != nil {
				return nil, err
			} else if missing != nil {
				continue
			}
			listed, err := agentdir.List(target.directory)
			if err != nil {
				return nil, err
			}
			for _, found := range listed {
				folders = append(folders, scopeFolder{Folder: found, target: target, skillID: claimed[folder{row.scope(), found.Name}]})
			}
		}
	}
	return folders, nil
}

// FetchUnmanagedRequest 是对作用域里一个不是库装的技能入库:ListUnmanagedSkills 列出的一条,按作用域与文件夹名指定。
// SourceInput 为空时作为没有来源的技能入库;给出时按这个来源入库,进它的集合,照常检查更新、更新与回滚。
type FetchUnmanagedRequest struct {
	Scope Scope  `json:"scope"`
	Name  string `json:"name"` // UnmanagedSkill.Name,即技能文件夹名
	// SourceInput 是技能的来源,格式与识别规则同 PreviewFetchRequest.Input;入库确认里预填 UnmanagedSkill.SourceInput,
	// 用户可以改成别的来源链接,或清空。来源里按 SKILL.md 里的 name 找这个技能。
	SourceInput string `json:"source_input,omitempty"`
	// SourceRef 是来源的分支或 tag,同 PreviewFetchRequest.Ref,只对 Git 类来源有意义;预填 UnmanagedSkill.SourceRef。
	SourceRef string `json:"source_ref,omitempty"`
}

// FetchUnmanagedResult 是从作用域入库的结果。
type FetchUnmanagedResult struct {
	// Skill 是记为装在这个作用域的库里的技能:新入库的技能,或 Matched 时内容一致的那个技能。
	Skill Skill `json:"skill"`
	// Matched 为 true 表示文件夹的内容和库里这个技能的某个版本完全一致:没有新建技能,只给它补记了这一处安装。
	Matched bool `json:"matched,omitzero"`
}

// FetchUnmanaged 把作用域里一个不是库装的技能入库,交给库管:磁盘上什么都不改(文件夹是符号链接时照样是符号链接),
// 读出文件夹的内容(文件夹是符号链接时读它指向的内容,受入库的读取上限约束)成为库里的版本,并记为直接安装在这个作用域;
// 入库后巡检这一处与库里的版本一致。
// 文件夹名和技能名不同时,这一处按原来的文件夹名记录(SkillScope.Folder),之后重新安装才换成技能名。
// 入库之后,用户在库外(如命令行工具)再改这个文件夹,巡检报为 modified;提交在项目仓库里的文件夹也能入库,
// 之后在 app 里更新、重新安装它会改动仓库里的文件。确认框据此提醒。
//
// 没有 SourceInput 时:内容和库里某个技能的某个版本完全一致(文件路径、内容与可执行位都相同)时,不新建技能,只给那个技能补记这一处安装
// (Matched;和它的当前版本一致的优先,其次是最新的旧版本,巡检相应为 current 或 stale);已经装在这个作用域的技能不算。
// 否则新建一个没有来源的技能(见 Skill),名字取 SKILL.md frontmatter 的 name,描述取 description。
//
// 有 SourceInput 时,先按入库预览的规则识别并读取来源(联网期间不持库锁),在来源里按 SKILL.md 的 name 取这个技能,
// 再进这个来源的集合(库里已有这个来源时进已有的集合),和入库一样按整个来源记下当时有哪些技能,都算用户见过:
//   - 集合里已有同名技能:内容和它的某个版本完全一致时只补记这一处安装(Matched),否则返回 AlreadyExists。
//     这里只在这个技能里找内容一致的版本,不跨到别的集合或没有来源的技能。
//   - 内容和来源里的一致:新技能的版本就是来源里的那一版,带来源版本(Skill.Revision),检查更新照常。
//   - 内容和来源里的不一样:新技能的版本就是磁盘上的内容。下面说的来源记录是 npx skills 的来源记录(见 UnmanagedSkill.SourceInput),
//     只在请求里的来源就是来源记录认出的来源时用它:
//   - 来源记录里记的哈希和磁盘内容对得上:能确认这是当初装的原样、只是落后于作者,作为作者的旧版,来源版本未知(Revision 为空),
//     第一次检查更新就按内容报为有新版本。
//   - 来源记录里记的哈希对不上磁盘内容,却和来源里现在的这个技能对得上,两边共有的文件可执行位也都一样:装的就是来源现在这一版,
//     磁盘不一样是用户之后改的(项目记录里的哈希不算可执行位,可执行位不一样时说不清,归到下面的其余情况)。
//     先记下来源里的那一版,再把磁盘上的内容记为基于它的修改(SkillVersion.BasedOn,来源版本沿用它的),
//     和用目录里的修改更新之后一样:检查更新只看来源从那一版之后有没有改过,更新计划里这个技能为 PlannedSkill.Edited。
//   - 其余情况(没有来源记录、记录里没有哈希、哈希哪一份都对不上):说不清有没有用户的修改,来源版本未知,版本标为
//     SkillVersion.MaybeEdited,第一次检查更新就按内容报为有新版本,更新计划里这个技能为 PlannedSkill.Edited,更新会换掉这份修改。
//
// 错误:作用域不合法、Name 不是合法的文件夹名返回 InvalidArgument(Details.Field 为 scope / name);Agent、项目不存在返回 NotFound;
// 作用域根目录不存在返回 FailedPrecondition(Details.Path 为根目录,项目带 ProjectID);技能目录里没有这个技能文件夹(不存在,
// 或根上没有 SKILL.md)返回 NotFound(Details.Path 为文件夹);这一处已经是库装的(这个作用域或共用目录的 Agent 有安装记录用它)
// 返回 FailedPrecondition(Details.Path 为文件夹,Details.SkillID 为装在这里的技能);文件夹里有不是普通文件的东西返回
// FailedPrecondition(Details.Path);SKILL.md 不是有效的技能描述(缺 frontmatter、name 或 description)或超出读取上限
// 返回 SourceInvalid(Details.Path 为文件夹)。
// 有 SourceInput 时另有:来源输入无效、或 SourceRef 用在不是 Git 类的来源上返回 InvalidArgument(Details.Field 为 source_input / source_ref;
// 只给 SourceRef 时为 source_ref);来源连不上返回 SourceUnreachable;来源里没有这个技能返回 NotFound(Details.Name 为技能名);
// 读来源期间文件夹里的技能名变了返回 FailedPrecondition(Details.Path 为文件夹);集合里已有同名技能、内容又和它的哪个版本都对不上返回
// AlreadyExists(Details.SkillID 为那个技能,Details.Name 为技能名)。失败时库和磁盘都不变。
func (l *Library) FetchUnmanaged(ctx context.Context, req FetchUnmanagedRequest) (result FetchUnmanagedResult, err error) {
	defer func() { err = typedError(err) }()
	const operation = "fetch unmanaged skill"
	if !validFolderName(req.Name) {
		return FetchUnmanagedResult{}, argumentError(operation, "name", "invalid skill folder name")
	}
	input := strings.TrimSpace(req.SourceInput)
	if input == "" {
		if req.SourceRef != "" {
			return FetchUnmanagedResult{}, argumentError(operation, "source_ref", "a ref needs a source input")
		}
		ctx, unlock := l.exclusiveOperation(ctx)
		defer unlock()
		folder, err := l.unmanagedFolder(ctx, operation, req)
		if err != nil {
			return FetchUnmanagedResult{}, err
		}
		return l.fetchWithoutSource(ctx, []unmanagedFolder{folder})
	}
	before, err := l.lockedUnmanagedFolder(ctx, operation, req)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	opened, err := l.openSource(ctx, input, req.SourceRef)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	sourced, err := opened.skill(ctx, before.parsed.Name)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	folder, err := l.unmanagedFolder(ctx, operation, req)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	if folder.parsed.Name != before.parsed.Name {
		return FetchUnmanagedResult{}, preconditionError(operation, "the skill folder changed while the source was read", ErrorDetails{Path: folder.path})
	}
	return l.fetchWithSource(ctx, operation, []unmanagedFolder{folder}, sourced, input, req.SourceRef)
}

func sameModes(files, others []skillfile.File) bool {
	modes := make(map[string]skillfile.Mode, len(others))
	for _, file := range others {
		modes[file.Path] = file.Mode
	}
	for _, file := range files {
		if mode, ok := modes[file.Path]; ok && mode != file.Mode {
			return false
		}
	}
	return true
}

func validFolderName(name string) bool {
	return discovery.ValidName(name) && !strings.HasPrefix(name, ".")
}

type unmanagedFolder struct {
	target resolvedScope
	name   string
	path   string
	files  []skillfile.File
	parsed discovery.Candidate
}

func (l *Library) lockedUnmanagedFolder(ctx context.Context, operation string, req FetchUnmanagedRequest) (unmanagedFolder, error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	return l.unmanagedFolder(ctx, operation, req)
}

func (l *Library) unmanagedFolder(ctx context.Context, operation string, req FetchUnmanagedRequest) (unmanagedFolder, error) {
	target, err := l.resolveScope(ctx, operation, req.Scope)
	if err != nil {
		return unmanagedFolder{}, err
	}
	if err := target.requireRoot(operation); err != nil {
		return unmanagedFolder{}, err
	}
	path := filepath.Join(target.directory, req.Name)
	folders, err := agentdir.List(target.directory)
	if err != nil {
		return unmanagedFolder{}, err
	}
	if !slices.ContainsFunc(folders, func(found agentdir.Folder) bool { return found.Name == req.Name }) {
		return unmanagedFolder{}, &Error{Code: NotFound, Message: operation + ": no skill folder with this name in the scope", Details: ErrorDetails{Path: path}}
	}
	installed, err := l.listSkillScopes(ctx, 0, &target.scopeRow)
	if err != nil {
		return unmanagedFolder{}, err
	}
	for _, skillScope := range installed {
		if filepath.Base(skillScope.Folder) == req.Name {
			return unmanagedFolder{}, preconditionError(operation, "the folder is installed by the library", ErrorDetails{Path: path, SkillID: skillScope.SkillID})
		}
	}
	files, parsed, err := readSkillFolder(ctx, operation, path, req.Name)
	if err != nil {
		return unmanagedFolder{}, err
	}
	return unmanagedFolder{target: target, name: req.Name, path: path, files: files, parsed: parsed}, nil
}

func readSkillFolder(ctx context.Context, operation, path, name string) ([]skillfile.File, discovery.Candidate, error) {
	files, err := agentdir.ReadFiles(ctx, path, maxFetchFiles, maxFetchSize)
	switch {
	case errors.Is(err, agentdir.ErrIrregularFile):
		return nil, discovery.Candidate{}, preconditionError(operation, "the folder has entries that are not regular files", ErrorDetails{Path: path})
	case errors.Is(err, agentdir.ErrTooLarge):
		return nil, discovery.Candidate{}, &Error{Code: SourceInvalid, Message: operation + ": " + err.Error(), Details: ErrorDetails{Path: path}, Err: err}
	case err != nil:
		return nil, discovery.Candidate{}, err
	}
	index := slices.IndexFunc(files, func(file skillfile.File) bool { return file.Path == "SKILL.md" })
	if index < 0 {
		return nil, discovery.Candidate{}, &Error{Code: NotFound, Message: operation + ": the folder has no SKILL.md", Details: ErrorDetails{Path: path}}
	}
	parsed, err := discovery.ParseSkillMD(files[index].Content, name)
	if err != nil {
		return nil, discovery.Candidate{}, &Error{Code: SourceInvalid, Message: operation + ": SKILL.md: " + err.Error(), Details: ErrorDetails{Path: path}, Err: err}
	}
	return files, parsed, nil
}

func (l *Library) fetchWithoutSource(ctx context.Context, folders []unmanagedFolder) (FetchUnmanagedResult, error) {
	folder := folders[0]
	matched, err := matchingSkill(ctx, l.store, folder.files, folders, 0)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	var row store.Skill
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		now := clockNow()
		id := matched
		if id == 0 {
			inserted, _, err := tx.InsertSkill(ctx, 0, folder.parsed.Name, store.NewVersion{Description: folder.parsed.Description, Files: folder.files, CreatedAt: now})
			if err != nil {
				return err
			}
			id = inserted
		}
		if err := recordFetchedFolders(ctx, tx, id, folders, now); err != nil {
			return err
		}
		var err error
		row, err = tx.GetSkill(ctx, id)
		return err
	})
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	skill, err := l.skillOf(row)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	return FetchUnmanagedResult{Skill: skill, Matched: matched != 0}, nil
}

type sourcedSkill struct {
	record   store.Source
	skill    source.Skill
	revision string
	names    []string
}

type openedSource struct {
	target   source.Target
	snapshot source.Snapshot
	names    []string
}

func (l *Library) openSource(ctx context.Context, input, ref string) (*openedSource, error) {
	target, field, err := l.previewTarget(ctx, PreviewFetchRequest{Input: input, Ref: ref})
	if err != nil {
		return nil, sourceField(inputField(err, field))
	}
	snapshot := target.Snapshot
	if snapshot == nil {
		if snapshot, err = l.sources.Open(ctx, target.Type, target.Locator, target.Subpath, nil); err != nil {
			return nil, sourceField(inputField(err, "input"))
		}
	}
	return &openedSource{target: target, snapshot: snapshot}, nil
}

func (o *openedSource) skill(ctx context.Context, name string) (sourcedSkill, error) {
	if len(o.target.Names) > 0 && !slices.Contains(o.target.Names, name) {
		return sourcedSkill{}, &source.NotFoundError{What: source.MissingSkill, Name: name}
	}
	skills, err := o.snapshot.Fetch(ctx, []string{name})
	if err != nil {
		return sourcedSkill{}, err
	}
	if o.names == nil {
		if o.names, err = o.snapshot.Names(ctx); err != nil {
			return sourcedSkill{}, err
		}
	}
	return sourcedSkill{record: store.Source{Type: o.target.Type, Locator: o.target.Locator}, skill: skills[0], revision: o.snapshot.Revision(), names: o.names}, nil
}

func sourceField(err error) error {
	if typed, ok := errors.AsType[*Error](err); ok && typed.Code == InvalidArgument {
		switch typed.Details.Field {
		case "", "input":
			typed.Details.Field = "source_input"
		case "ref":
			typed.Details.Field = "source_ref"
		}
	}
	return err
}

func (l *Library) fetchWithSource(ctx context.Context, operation string, folders []unmanagedFolder, sourced sourcedSkill, input, ref string) (FetchUnmanagedResult, error) {
	folder := folders[0]
	untouched, editedLatest := false, false
	records := l.sourceRecords()
	for _, each := range folders {
		if entry, ok := records.find(each.target, each.parsed.Name, each.name); ok {
			recordedInput, recordedRef, recognized := entry.Input()
			if !recognized || recordedInput != input || recordedRef != ref {
				continue
			}
			matches := entry.Matches(each.files)
			untouched = untouched || matches
			editedLatest = editedLatest || !matches && entry.Matches(sourced.skill.Files) && sameModes(each.files, sourced.skill.Files)
		}
	}
	name := sourced.skill.Name
	var row store.Skill
	var matched bool
	err := l.store.InTx(ctx, func(tx *store.Tx) error {
		now := clockNow()
		sourceID, err := tx.UpsertSource(ctx, sourced.record)
		if err != nil {
			return err
		}
		if err := tx.ListSourceSkills(ctx, sourceID, append(sourced.names, name), sourced.revision, true); err != nil {
			return err
		}
		id, err := tx.FindSkill(ctx, sourceID, name)
		if err != nil {
			return err
		}
		if id != 0 {
			found, err := matchingSkill(ctx, tx, folder.files, folders, id)
			if err != nil {
				return err
			}
			if found == 0 {
				return existsError(operation, "the collection already has a skill with this name and different content", ErrorDetails{SkillID: SkillID(id), Name: name})
			}
			matched = true
		} else {
			version := store.NewVersion{Description: folder.parsed.Description, Files: folder.files, CreatedAt: now}
			switch {
			case skillfile.Equal(folder.files, sourced.skill.Files):
				version.State, version.Revision = string(sourced.skill.State), sourced.revision
			case editedLatest && !untouched:
				base := store.NewVersion{Description: sourced.skill.Description, Files: sourced.skill.Files, State: string(sourced.skill.State), Revision: sourced.revision, CreatedAt: now}
				if id, version.Base, err = tx.InsertSkill(ctx, sourceID, name, base); err != nil {
					return err
				}
				version.Revision = sourced.revision
			default:
				version.MaybeEdited = !untouched
			}
			if id == 0 {
				if id, _, err = tx.InsertSkill(ctx, sourceID, name, version); err != nil {
					return err
				}
			} else if _, err = tx.AddVersion(ctx, id, version); err != nil {
				return err
			}
		}
		if err := recordFetchedFolders(ctx, tx, id, folders, now); err != nil {
			return err
		}
		row, err = tx.GetSkill(ctx, id)
		return err
	})
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	skill, err := l.skillOf(row)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	return FetchUnmanagedResult{Skill: skill, Matched: matched}, nil
}

// PlanFetchUnmanagedRequest 指定整批入库要看哪些作用域。
type PlanFetchUnmanagedRequest struct {
	// Scopes 是要看的作用域:第一次打开 app 时是各 Agent 的全局,登记一个项目之后是各 Agent 在这个项目里。重复的只算一次。
	Scopes []Scope `json:"scopes"`
}

// PlannedUnmanagedSkill 是整批入库计划里的一个技能:这几个作用域里内容完全一致(文件路径、内容与可执行位都相同)的
// 不是库装的技能文件夹合成一条,入库成库里的一个技能,各处都记为安装。
type PlannedUnmanagedSkill struct {
	// Name 是 SKILL.md 里的 name;Error 不为空时是文件夹名。
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Folders 是内容一致的各处,与 ListUnmanagedSkills 列出的条目相同,按 Agent、作用域排序;几个 Agent 共用的文件夹每个 Agent 各一条。
	// 一个技能在一个作用域里只记一处安装,所以每个作用域至多一处:同一个作用域里内容一样的另一个文件夹是另一条。
	Folders []UnmanagedSkill `json:"folders"`
	// SourceInput 与 SourceRef 是从来源记录认出的来源(见 UnmanagedSkill.SourceInput),取第一处认出的;入库确认里预填,
	// 界面按它分组,认不出的单独一组。
	SourceInput string `json:"source_input,omitempty"`
	SourceRef   string `json:"source_ref,omitempty"`
	// InRepository、InProjectLock 表示其中有一处提交在项目仓库里、记在项目根目录的 skills-lock.json 里
	// (见 UnmanagedSkill 的同名字段)。两者都归项目仓库管,默认不勾。
	InRepository  bool `json:"in_repository,omitzero"`
	InProjectLock bool `json:"in_project_lock,omitzero"`
	// LinkOutside 表示其中有一处是指向 Agent 目录以外的符号链接(见 UnmanagedSkill.LinkOutside),默认不勾。
	LinkOutside bool `json:"link_outside,omitzero"`
	// ModifiedAt 是各处文件夹里最近一次修改文件的时间(文件夹是符号链接时看它指向的内容)。
	ModifiedAt time.Time `json:"modified_at,omitzero"`
	// Conflict 表示计划里还有同名的另一条(内容不一样,或同一个作用域里内容一样的另一个文件夹)。同名的几条只该入库一条:
	// 默认勾最近修改的那条,其余的入库后仍是未纳管技能,之后想要再入库。
	Conflict bool `json:"conflict,omitzero"`
	// Selected 是默认是否勾选:没有 Error、不在项目仓库里、没记在 skills-lock.json 里、不是指向 Agent 目录以外的符号链接,
	// 同名冲突时又是最近修改的那条。
	Selected bool `json:"selected,omitzero"`
	// Error 是这个文件夹不能入库的原因,错误码同 FetchUnmanaged 读文件夹时的错误(文件夹名不能用为 InvalidArgument,
	// 文件夹里有不是普通文件的东西为 FailedPrecondition,SKILL.md 不是有效的技能描述或超出读取上限为 SourceInvalid,
	// 读不了为 Permission 等),Details.Path 为文件夹;这时 Folders 只有这一处,不和别处合并,也不算同名冲突。
	// 一个文件夹读不成不影响计划里的其余各条。
	Error *Error `json:"error,omitempty"`
}

// PlanFetchUnmanaged 是整批入库前的确认:列出这几个作用域里不是库装的技能文件夹,内容一致的合成一条,标出同名冲突与默认勾选。
// 第一次打开 app 时对各 Agent 的全局、登记一个项目之后对这个项目各 Agent 的作用域调用它,用户确认后交给 FetchUnmanagedSkills。
// 技能文件夹的认法与 ListUnmanagedSkills 相同,并读出每个文件夹的全部内容比较。不记哪些作用域看过:之后什么时候调用都是现查。
// 只读:不改库、不改磁盘。结果按 Name 排序,同名的按 ModifiedAt 从新到旧。
// 错误:作用域不合法返回 InvalidArgument(Details.Field 指向对应字段),Agent、项目不存在返回 NotFound。
func (l *Library) PlanFetchUnmanaged(ctx context.Context, req PlanFetchUnmanagedRequest) (plan []PlannedUnmanagedSkill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "plan fetch unmanaged skills"
	wanted := map[Scope]bool{}
	for _, scope := range req.Scopes {
		if _, err := l.resolveScope(ctx, operation, scope); err != nil {
			return nil, err
		}
		wanted[scope] = true
	}
	entries, err := l.unmanagedSkills(ctx, wanted)
	if err != nil {
		return nil, err
	}
	plan = []PlannedUnmanagedSkill{}
	byContent := map[string][]int{}
	for _, entry := range entries {
		files, parsed, modified, err := readPlannedFolder(ctx, operation, entry)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failure := ErrorOf(err)
			if failure.Details.Path == "" {
				failure.Details.Path = entry.Folder
			}
			plan = append(plan, PlannedUnmanagedSkill{Name: entry.Name, Description: entry.Description, Folders: []UnmanagedSkill{entry.UnmanagedSkill}, Error: failure})
			continue
		}
		sum := skillfile.Sum(files)
		same := slices.IndexFunc(byContent[sum], func(index int) bool {
			return !slices.ContainsFunc(plan[index].Folders, func(folder UnmanagedSkill) bool { return folder.Scope == entry.Scope })
		})
		if same >= 0 {
			planned := &plan[byContent[sum][same]]
			planned.Folders = append(planned.Folders, entry.UnmanagedSkill)
			planned.InRepository = planned.InRepository || entry.InRepository
			planned.InProjectLock = planned.InProjectLock || entry.InProjectLock
			planned.LinkOutside = planned.LinkOutside || entry.LinkOutside
			if modified.After(planned.ModifiedAt) {
				planned.ModifiedAt = modified
			}
			if planned.SourceInput == "" {
				planned.SourceInput, planned.SourceRef = entry.SourceInput, entry.SourceRef
			}
			continue
		}
		byContent[sum] = append(byContent[sum], len(plan))
		plan = append(plan, PlannedUnmanagedSkill{
			Name: parsed.Name, Description: parsed.Description, Folders: []UnmanagedSkill{entry.UnmanagedSkill},
			SourceInput: entry.SourceInput, SourceRef: entry.SourceRef,
			InRepository: entry.InRepository, InProjectLock: entry.InProjectLock, LinkOutside: entry.LinkOutside, ModifiedAt: modified,
		})
	}
	newest := map[string]int{}
	for index, planned := range plan {
		if planned.Error != nil {
			continue
		}
		other, ok := newest[planned.Name]
		if !ok {
			newest[planned.Name] = index
			continue
		}
		plan[index].Conflict, plan[other].Conflict = true, true
		if planned.ModifiedAt.After(plan[other].ModifiedAt) {
			newest[planned.Name] = index
		}
	}
	for index := range plan {
		planned := &plan[index]
		planned.Selected = planned.Error == nil && !planned.InRepository && !planned.InProjectLock && !planned.LinkOutside && newest[planned.Name] == index
	}
	slices.SortStableFunc(plan, func(a, b PlannedUnmanagedSkill) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), b.ModifiedAt.Compare(a.ModifiedAt))
	})
	return plan, nil
}

func readPlannedFolder(ctx context.Context, operation string, entry unmanagedEntry) ([]skillfile.File, discovery.Candidate, time.Time, error) {
	if !validFolderName(entry.Name) {
		return nil, discovery.Candidate{}, time.Time{}, &Error{Code: InvalidArgument, Message: operation + ": invalid skill folder name", Details: ErrorDetails{Field: "name", Path: entry.Folder}}
	}
	files, parsed, err := readSkillFolder(ctx, operation, entry.Folder, entry.Name)
	if err != nil {
		return nil, discovery.Candidate{}, time.Time{}, err
	}
	modified, err := agentdir.Modified(ctx, entry.Folder)
	return files, parsed, modified, err
}

// FolderInScope 是作用域里的一个技能文件夹,按作用域与文件夹名(UnmanagedSkill.Name)指定。
type FolderInScope struct {
	Scope Scope  `json:"scope"`
	Name  string `json:"name"`
}

// UnmanagedFetchItem 是整批入库里的一个技能:内容完全一致的几处文件夹(通常取自 PlannedUnmanagedSkill.Folders)和它的来源。
// SourceInput、SourceRef 的规则同 FetchUnmanagedRequest:预填 PlannedUnmanagedSkill 认出的来源,用户可以改链接或清空。
type UnmanagedFetchItem struct {
	Folders     []FolderInScope `json:"folders"`
	SourceInput string          `json:"source_input,omitempty"`
	SourceRef   string          `json:"source_ref,omitempty"`
}

// FetchUnmanagedSkillsRequest 是一次确认的整批入库:用户勾选的各个技能。
type FetchUnmanagedSkillsRequest struct {
	Skills []UnmanagedFetchItem `json:"skills"`
}

// FetchedUnmanagedSkill 是整批入库里一个技能的结果,与请求里的一项一一对应。
type FetchedUnmanagedSkill struct {
	// Folders 原样取自请求。
	Folders []FolderInScope `json:"folders"`
	// Skill 是记为装在各处的库里的技能,同 FetchUnmanagedResult.Skill;Error 不为空时为零值。
	Skill Skill `json:"skill,omitzero"`
	// Matched 同 FetchUnmanagedResult.Matched:内容和库里这个技能的某个版本一致,只补记了安装。
	Matched bool `json:"matched,omitzero"`
	// Error 是这个技能没入库的原因,错误码同 FetchUnmanaged;这时库里没有记下它的任何一处,它仍是未纳管技能。
	// 另有:几处文件夹的内容不一样返回 FailedPrecondition(Details.Path 为对不上的那个文件夹)。
	Error *Error `json:"error,omitempty"`
}

// FetchUnmanagedSkills 一次确认把几个不是库装的技能入库,每个技能的规则和 FetchUnmanaged 相同:磁盘上什么都不改,
// 读出的内容成为库里的版本,记为装在各处;带来源的进这个来源的集合,
// 不带来源时和库里某个版本完全一致只补记安装。
// 同一个来源(SourceInput 与 SourceRef 都相同)只识别、读取一次,读来源期间不持库锁。一个技能失败不影响其余的:
// 每个技能各自成功或失败,结果按请求顺序逐条返回,失败的写在 FetchedUnmanagedSkill.Error 里,用户可以改来源或清空后单独重试。
// 同名不同内容的几份都勾上时照样逐个入库,规则同 FetchUnmanaged(带同一来源的后一份返回 AlreadyExists),只勾一份由调用方决定。
// 错误:某项没有文件夹、同一个文件夹出现在几项里、一项里有两处在同一个作用域返回 InvalidArgument(Details.Field 为 folders),文件夹名不合法为 name,
// 只给 SourceRef 为 source_ref;这时什么都不做。
func (l *Library) FetchUnmanagedSkills(ctx context.Context, req FetchUnmanagedSkillsRequest) (results []FetchedUnmanagedSkill, err error) {
	defer func() { err = typedError(err) }()
	const operation = "fetch unmanaged skills"
	seen := map[FolderInScope]bool{}
	for _, item := range req.Skills {
		if len(item.Folders) == 0 {
			return nil, argumentError(operation, "folders", "each skill needs at least one folder")
		}
		scopes := map[Scope]bool{}
		for _, folder := range item.Folders {
			if !validFolderName(folder.Name) {
				return nil, argumentError(operation, "name", "invalid skill folder name")
			}
			if seen[folder] {
				return nil, argumentError(operation, "folders", "a folder is given more than once")
			}
			if scopes[folder.Scope] {
				return nil, argumentError(operation, "folders", "a skill is installed once per scope")
			}
			seen[folder], scopes[folder.Scope] = true, true
		}
		if strings.TrimSpace(item.SourceInput) == "" && item.SourceRef != "" {
			return nil, argumentError(operation, "source_ref", "a ref needs a source input")
		}
	}
	results = make([]FetchedUnmanagedSkill, len(req.Skills))
	names := make([]string, len(req.Skills))
	for index, item := range req.Skills {
		results[index].Folders = item.Folders
		if strings.TrimSpace(item.SourceInput) == "" {
			continue
		}
		first := item.Folders[0]
		folder, err := l.lockedUnmanagedFolder(ctx, operation, FetchUnmanagedRequest{Scope: first.Scope, Name: first.Name})
		if err != nil {
			results[index].Error = ErrorOf(err)
			continue
		}
		names[index] = folder.parsed.Name
	}
	type sourceKey struct{ input, ref string }
	opened := map[sourceKey]*openedSource{}
	failed := map[sourceKey]error{}
	sourced := make([]sourcedSkill, len(req.Skills))
	for index, item := range req.Skills {
		input := strings.TrimSpace(item.SourceInput)
		if input == "" || results[index].Error != nil {
			continue
		}
		key := sourceKey{input, item.SourceRef}
		if opened[key] == nil && failed[key] == nil {
			if opened[key], err = l.openSource(ctx, input, item.SourceRef); err != nil {
				failed[key] = err
			}
		}
		if failed[key] != nil {
			results[index].Error = ErrorOf(failed[key])
			continue
		}
		if sourced[index], err = opened[key].skill(ctx, names[index]); err != nil {
			results[index].Error = ErrorOf(err)
		}
	}
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	for index, item := range req.Skills {
		if results[index].Error != nil {
			continue
		}
		fetched, err := l.fetchUnmanagedItem(ctx, operation, item, names[index], sourced[index])
		if err != nil {
			results[index].Error = ErrorOf(err)
			continue
		}
		results[index].Skill, results[index].Matched = fetched.Skill, fetched.Matched
	}
	return results, nil
}

func (l *Library) fetchUnmanagedItem(ctx context.Context, operation string, item UnmanagedFetchItem, name string, sourced sourcedSkill) (FetchUnmanagedResult, error) {
	folders := make([]unmanagedFolder, 0, len(item.Folders))
	for _, each := range item.Folders {
		folder, err := l.unmanagedFolder(ctx, operation, FetchUnmanagedRequest{Scope: each.Scope, Name: each.Name})
		if err != nil {
			return FetchUnmanagedResult{}, err
		}
		if len(folders) > 0 && !skillfile.Equal(folder.files, folders[0].files) {
			return FetchUnmanagedResult{}, preconditionError(operation, "the folders have different content", ErrorDetails{Path: folder.path})
		}
		folders = append(folders, folder)
	}
	input := strings.TrimSpace(item.SourceInput)
	if input == "" {
		return l.fetchWithoutSource(ctx, folders)
	}
	if folders[0].parsed.Name != name {
		return FetchUnmanagedResult{}, preconditionError(operation, "the skill folder changed while the source was read", ErrorDetails{Path: folders[0].path})
	}
	return l.fetchWithSource(ctx, operation, folders, sourced, input, item.SourceRef)
}

type versionReader interface {
	VersionsWithFile(ctx context.Context, path, fingerprint string) ([]store.VersionMatch, error)
	PlaceSkillScopes(ctx context.Context, skillID int64, scope store.Scope) ([]store.PlaceSkillScope, error)
	VersionDigests(ctx context.Context, versionID int64) (map[string]skillfile.Digest, error)
}

func recordFetchedFolders(ctx context.Context, tx *store.Tx, id int64, folders []unmanagedFolder, now time.Time) error {
	for _, folder := range folders {
		if err := tx.RecordFetchedSkillScope(ctx, id, folder.target.record(), folder.name, now); err != nil {
			return err
		}
	}
	return nil
}

func matchingSkill(ctx context.Context, reader versionReader, files []skillfile.File, folders []unmanagedFolder, only int64) (int64, error) {
	disk := digests(files)
	manifest, ok := disk["SKILL.md"]
	if !ok {
		return 0, nil
	}
	matches, err := reader.VersionsWithFile(ctx, "SKILL.md", manifest.Fingerprint)
	if err != nil {
		return 0, err
	}
next:
	for _, match := range matches {
		if only != 0 && match.SkillID != only {
			continue
		}
		for _, folder := range folders {
			place, err := reader.PlaceSkillScopes(ctx, match.SkillID, folder.target.record())
			if err != nil {
				return 0, err
			}
			if len(place) > 0 {
				continue next
			}
		}
		version, err := reader.VersionDigests(ctx, match.VersionID)
		if err != nil {
			return 0, err
		}
		if maps.Equal(disk, version) {
			return match.SkillID, nil
		}
	}
	return 0, nil
}

type sourceRecords struct {
	home string
	read map[string]skilllock.Records
}

func (l *Library) sourceRecords() *sourceRecords {
	return &sourceRecords{home: l.home, read: map[string]skilllock.Records{}}
}

func (f *sourceRecords) find(target resolvedScope, names ...string) (skilllock.Record, bool) {
	root := ""
	if target.projectID != nil {
		root = target.root
	}
	records, ok := f.read[root]
	if !ok {
		if root == "" {
			records = skilllock.GlobalRecords(f.home)
		} else {
			records = skilllock.ProjectRecords(root)
		}
		f.read[root] = records
	}
	return records.Find(names...)
}

// FindUnmanagedSourcesRequest 指定要去 skills.sh 找来源的未纳管技能。
type FindUnmanagedSourcesRequest struct {
	// Folders 是 ListUnmanagedSkills 列出的条目,按作用域与文件夹名指定,重复的只算一次。
	// 来源记录已经认出来源(UnmanagedSkill.SourceInput 不为空)的,和已经不是未纳管技能、不在了的一样,不找也不报错。
	Folders []FolderInScope `json:"folders"`
}

// FoundUnmanagedSource 是在 skills.sh 上给一个未纳管技能找到的来源。
type FoundUnmanagedSource struct {
	Scope Scope  `json:"scope"`
	Name  string `json:"name"` // UnmanagedSkill.Name,即技能文件夹名
	// SourceInput 是找到的来源,和来源记录认出的 UnmanagedSkill.SourceInput 一样用:界面写「来自 …」,入库确认里预填给
	// FetchUnmanagedRequest.SourceInput 与 UnmanagedFetchItem.SourceInput,整批确认按它分组。格式同 PreviewFetchRequest.Input:
	// GitHub 仓库为 owner/repo,well-known 站点为 https://<站点>;不带分支,SourceRef 留空。
	SourceInput string `json:"source_input"`
}

// FindUnmanagedSources 给来源记录认不出来源的未纳管技能去 skills.sh 找来源:按 SKILL.md 里的 name 搜索,只看名字和它相同、
// 能转成入库预览输入的结果(规则同 SearchSkills),读出来源里这个技能的文件,和磁盘上的内容比较相似度,
// 得分不低于 60% 的取得分最高的那个,一样高时取 skills.sh 排在前面的。相似度的算法和推测来源里改名的技能相同:
// 同路径的文件按非空行的重合比例算分(SKILL.md 不算 name 那一行),相加后除以两边文件数中较多的那个。
// 用找到的来源入库(FetchUnmanaged、FetchUnmanagedSkills)时,内容和来源里的一致就是来源里的那一版,不一致时和用户自己给的来源一样,
// 版本标为 SkillVersion.MaybeEdited。
// 它要联网,ListUnmanagedSkills 与 PlanFetchUnmanaged 不等它:界面列出之后在后台调用,结果到了再补上。
// 搜索 skills.sh 与读来源加起来最多同时 4 个;同一个来源这一次只读一次,几个技能共用。联网期间不持库锁。
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
				results, err := market.Search(ctx, name)
				if err != nil {
					return
				}
				var inputs []string
				for _, result := range results {
					if _, input, ok := searchInputs(result); ok && result.Name == name && !slices.Contains(inputs, input) {
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
	for input, names := range wanted {
		wg.Go(func() {
			var opened *openedSource
			limit.do(ctx, func() { opened, _ = l.openSource(ctx, input, "") })
			if opened == nil {
				return
			}
			for _, name := range names {
				limit.do(ctx, func() {
					skills, err := opened.snapshot.Fetch(ctx, []string{name})
					if err != nil || len(skills) != 1 {
						return
					}
					mu.Lock()
					fetched[sourceSkill{input, name}] = skills[0].Files
					mu.Unlock()
				})
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	found = []FoundUnmanagedSource{}
	for _, folder := range folders {
		best, score := "", 0.0
		for _, input := range searched[folder.name] {
			files, ok := fetched[sourceSkill{input, folder.name}]
			if !ok {
				continue
			}
			if each := similarity(folder.files, files); each >= renameSimilarity && each > score {
				best, score = input, each
			}
		}
		if best != "" {
			found = append(found, FoundUnmanagedSource{Scope: folder.Scope, Name: folder.Name, SourceInput: best})
		}
	}
	return found, nil
}

const maxSourceSearches = 4

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
	wanted := map[Scope]bool{}
	for _, folder := range requested {
		if !validFolderName(folder.Name) {
			return nil, argumentError(operation, "name", "invalid skill folder name")
		}
		if _, err := l.resolveScope(ctx, operation, folder.Scope); err != nil {
			return nil, err
		}
		wanted[folder.Scope] = true
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	entries, err := l.unmanagedSkills(ctx, wanted)
	if err != nil {
		return nil, err
	}
	listed := map[FolderInScope]unmanagedEntry{}
	for _, entry := range entries {
		listed[FolderInScope{Scope: entry.Scope, Name: entry.Name}] = entry
	}
	var folders []sourcelessFolder
	for _, folder := range requested {
		entry, ok := listed[folder]
		if !ok || entry.SourceInput != "" {
			continue
		}
		delete(listed, folder)
		files, parsed, err := readSkillFolder(ctx, operation, entry.Folder, entry.Name)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		folders = append(folders, sourcelessFolder{FolderInScope: folder, name: parsed.Name, files: files})
	}
	return folders, nil
}
