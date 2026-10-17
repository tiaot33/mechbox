package library

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillslock"
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
	// InRepository 表示文件夹已提交在项目的 git 仓库里(在 HEAD 里),界面标明「在项目仓库里」。只出现在项目作用域;
	// 项目不是 git 仓库、还没有提交或本机没有 git 时为 false。
	InRepository bool `json:"in_repository,omitzero"`
	// SourceInput 是从 npx skills 留下的锁文件里认出的来源,格式同 PreviewFetchRequest.Input;入库确认里预填给
	// FetchUnmanagedRequest.SourceInput,界面写「来自 …」。认不出时为空,用户可以自己给一个来源链接。
	// 全局作用域读 ~/.agents/.skill-lock.json(设了 XDG_STATE_HOME 时为 $XDG_STATE_HOME/skills/.skill-lock.json,只认 version 3 及以上),
	// 项目作用域读项目根目录的 skills-lock.json;记录按技能名对上文件夹:先比 SKILL.md 里的 name,再比文件夹名。
	// 记录里的来源类型为 github、gitlab、git、well-known、download 与 local(项目锁里的相对路径按项目根解析)时认得出,其他的认不出。
	SourceInput string `json:"source_input,omitempty"`
	// SourceRef 是锁文件里记的分支或 tag,只对 Git 类来源(github、gitlab、git)有;预填给 FetchUnmanagedRequest.SourceRef。
	SourceRef string `json:"source_ref,omitempty"`
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
	folders, err := l.scopeFolders(ctx)
	if err != nil {
		return nil, err
	}
	type repository struct {
		projectID ProjectID
		relative  string
	}
	committed := map[repository]map[string]bool{}
	locks := l.lockFiles()
	skills = []UnmanagedSkill{}
	for _, found := range folders {
		if found.skillID != 0 {
			continue
		}
		target := found.target
		skill := UnmanagedSkill{Scope: target.scope(), Name: found.Name, Folder: found.Path, LinkTarget: found.Target}
		if candidate, err := discovery.ParseSkillMD(found.SkillMD, found.Name); err == nil {
			skill.Description = candidate.Description
		}
		if entry, ok := locks.find(target, skillName(found.Folder), found.Name); ok {
			skill.SourceInput, skill.SourceRef, _ = entry.Input()
		}
		if target.projectID != nil {
			key := repository{projectID: *target.projectID, relative: target.relative}
			if _, ok := committed[key]; !ok {
				if committed[key], err = agentdir.Committed(ctx, target.root, target.relative); err != nil {
					return nil, err
				}
			}
			skill.InRepository = committed[key][found.Name]
		}
		skills = append(skills, skill)
	}
	slices.SortFunc(skills, func(a, b UnmanagedSkill) int {
		return cmp.Or(cmp.Compare(a.Scope.Agent, b.Scope.Agent), cmp.Compare(a.Scope.ProjectID, b.Scope.ProjectID), cmp.Compare(a.Name, b.Name))
	})
	return skills, nil
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
	folders, err := l.scopeFolders(ctx)
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

func (l *Library) scopeFolders(ctx context.Context) ([]scopeFolder, error) {
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
//   - 内容和来源里的不一样:新技能的版本就是磁盘上的内容,来源版本未知(Revision 为空),第一次检查更新就按内容报为有新版本。
//     npx skills 的锁文件(见 UnmanagedSkill.SourceInput)里记的哈希和磁盘内容对得上、且请求里的来源就是锁文件认出的来源时,
//     能确认这是当初装的原样、只是落后于作者,作为作者的旧版;否则说不清有没有用户的修改,版本标为 SkillVersion.MaybeEdited,
//     更新计划里这个技能为 PlannedSkill.Edited,更新会换掉这份修改。
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
	if !discovery.ValidName(req.Name) || strings.HasPrefix(req.Name, ".") {
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
		return l.fetchWithoutSource(ctx, folder)
	}
	before, err := l.lockedUnmanagedFolder(ctx, operation, req)
	if err != nil {
		return FetchUnmanagedResult{}, err
	}
	sourced, err := l.readSourceSkill(ctx, input, req.SourceRef, before.parsed.Name)
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
	return l.fetchWithSource(ctx, operation, folder, sourced, input, req.SourceRef)
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
	files, err := agentdir.ReadFiles(ctx, path, maxFetchFiles, maxFetchSize)
	switch {
	case errors.Is(err, agentdir.ErrIrregularFile):
		return unmanagedFolder{}, preconditionError(operation, "the folder has entries that are not regular files", ErrorDetails{Path: path})
	case errors.Is(err, agentdir.ErrTooLarge):
		return unmanagedFolder{}, &Error{Code: SourceInvalid, Message: operation + ": " + err.Error(), Details: ErrorDetails{Path: path}, Err: err}
	case err != nil:
		return unmanagedFolder{}, err
	}
	index := slices.IndexFunc(files, func(file skillfile.File) bool { return file.Path == "SKILL.md" })
	if index < 0 {
		return unmanagedFolder{}, &Error{Code: NotFound, Message: operation + ": the folder has no SKILL.md", Details: ErrorDetails{Path: path}}
	}
	parsed, err := discovery.ParseSkillMD(files[index].Content, req.Name)
	if err != nil {
		return unmanagedFolder{}, &Error{Code: SourceInvalid, Message: operation + ": SKILL.md: " + err.Error(), Details: ErrorDetails{Path: path}, Err: err}
	}
	return unmanagedFolder{target: target, name: req.Name, path: path, files: files, parsed: parsed}, nil
}

func (l *Library) fetchWithoutSource(ctx context.Context, folder unmanagedFolder) (FetchUnmanagedResult, error) {
	matched, err := matchingSkill(ctx, l.store, folder.files, folder.target, 0)
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
		if err := tx.RecordFetchedSkillScope(ctx, id, folder.target.record(), folder.name, now); err != nil {
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

func (l *Library) readSourceSkill(ctx context.Context, input, ref, name string) (sourcedSkill, error) {
	target, field, err := l.previewTarget(ctx, PreviewFetchRequest{Input: input, Ref: ref})
	if err != nil {
		return sourcedSkill{}, sourceField(inputField(err, field))
	}
	if len(target.Names) > 0 && !slices.Contains(target.Names, name) {
		return sourcedSkill{}, &source.NotFoundError{What: source.MissingSkill, Name: name}
	}
	snapshot := target.Snapshot
	if snapshot == nil {
		if snapshot, err = l.sources.Open(ctx, target.Type, target.Locator, target.Subpath, nil); err != nil {
			return sourcedSkill{}, sourceField(inputField(err, "input"))
		}
	}
	skills, err := snapshot.Fetch(ctx, []string{name})
	if err != nil {
		return sourcedSkill{}, err
	}
	names, err := snapshot.Names(ctx)
	if err != nil {
		return sourcedSkill{}, err
	}
	return sourcedSkill{record: store.Source{Type: target.Type, Locator: target.Locator}, skill: skills[0], revision: snapshot.Revision(), names: names}, nil
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

func (l *Library) fetchWithSource(ctx context.Context, operation string, folder unmanagedFolder, sourced sourcedSkill, input, ref string) (FetchUnmanagedResult, error) {
	untouched := false
	if entry, ok := l.lockFiles().find(folder.target, folder.parsed.Name, folder.name); ok {
		lockInput, lockRef, recognized := entry.Input()
		untouched = recognized && lockInput == input && lockRef == ref && entry.Matches(folder.files)
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
			found, err := matchingSkill(ctx, tx, folder.files, folder.target, id)
			if err != nil {
				return err
			}
			if found == 0 {
				return existsError(operation, "the collection already has a skill with this name and different content", ErrorDetails{SkillID: SkillID(id), Name: name})
			}
			matched = true
		} else {
			version := store.NewVersion{Description: folder.parsed.Description, Files: folder.files, CreatedAt: now}
			if skillfile.Equal(folder.files, sourced.skill.Files) {
				version.State, version.Revision = string(sourced.skill.State), sourced.revision
			} else {
				version.MaybeEdited = !untouched
			}
			if id, _, err = tx.InsertSkill(ctx, sourceID, name, version); err != nil {
				return err
			}
		}
		if err := tx.RecordFetchedSkillScope(ctx, id, folder.target.record(), folder.name, now); err != nil {
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

type versionReader interface {
	VersionsWithFile(ctx context.Context, path, fingerprint string) ([]store.VersionMatch, error)
	PlaceSkillScopes(ctx context.Context, skillID int64, scope store.Scope) ([]store.PlaceSkillScope, error)
	VersionDigests(ctx context.Context, versionID int64) (map[string]skillfile.Digest, error)
}

func matchingSkill(ctx context.Context, reader versionReader, files []skillfile.File, target resolvedScope, only int64) (int64, error) {
	disk := digests(files)
	manifest, ok := disk["SKILL.md"]
	if !ok {
		return 0, nil
	}
	matches, err := reader.VersionsWithFile(ctx, "SKILL.md", manifest.Fingerprint)
	if err != nil {
		return 0, err
	}
	for _, match := range matches {
		if only != 0 && match.SkillID != only {
			continue
		}
		place, err := reader.PlaceSkillScopes(ctx, match.SkillID, target.record())
		if err != nil {
			return 0, err
		}
		if len(place) > 0 {
			continue
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

type lockFiles struct {
	home  string
	locks map[string]skillslock.Lock
}

func (l *Library) lockFiles() *lockFiles {
	return &lockFiles{home: l.home, locks: map[string]skillslock.Lock{}}
}

func (f *lockFiles) find(target resolvedScope, names ...string) (skillslock.Entry, bool) {
	root := ""
	if target.projectID != nil {
		root = target.root
	}
	lock, ok := f.locks[root]
	if !ok {
		if root == "" {
			lock = skillslock.Global(f.home)
		} else {
			lock = skillslock.Project(root)
		}
		f.locks[root] = lock
	}
	return lock.Find(names...)
}
