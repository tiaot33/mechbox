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
	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
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
	// 全局作用域读 ~/.agents/.skill-lock.json(不跟随 XDG_STATE_HOME,只认 version 3 及以上),
	// 项目作用域读项目根目录的 skills-lock.json;记录按技能名对上文件夹:先比 SKILL.md 里的 name,再比文件夹名。
	// 记录里的来源类型为 github、gitlab、git、well-known、download 与 local(项目里记录的相对路径按项目根解析)时认得出,其他的认不出。
	// SkillID 不为 0 时为空:它就是库里的那个技能,跟着那个技能的来源,来源记录让位于它。
	SourceInput string `json:"source_input,omitempty"`
	// SourceRef 是来源记录里记的分支或 tag,只对 Git 类来源(github、gitlab、git)有;预填给 FetchUnmanagedRequest.SourceRef。
	SourceRef string `json:"source_ref,omitempty"`
	// InProjectLock 表示项目根目录的 skills-lock.json 记了这个技能(认不认得出来源都算):技能是团队约定装的,文件夹本身不一定提交了,
	// 和 InRepository 一样归项目仓库管。只出现在项目作用域。
	InProjectLock bool `json:"in_project_lock,omitzero"`
	// SkillID 不为 0 时,文件夹的内容(文件路径、内容与可执行位都相同,Windows 本机的目录只比路径与内容)和库里这个技能的某个版本完全一致,VersionID 是那个版本:
	// 和当前版本一致的优先,其次是最新的旧版本。它就是库里的这个技能,只是这一处不是库装的,界面把它显示在这个技能下;
	// 入库(FetchUnmanaged、FetchUnmanagedSkills)只给这个技能补记这一处安装,不论带不带来源、带的是哪个来源。
	// 已经装在这个作用域的技能不算:一个技能在一个作用域只记一处安装。
	SkillID   SkillID   `json:"skill_id,omitzero"`
	VersionID VersionID `json:"version_id,omitzero"`
	// SourceLatestOf 不为 0 时,文件夹的内容和库里这个技能在来源里现在的那一版完全一致(最近一次检查更新看到的、比库里新的那一版):
	// 是这个技能的新版,在库外更新过。按来源入库会因集合里已有同名技能返回 AlreadyExists;界面引导用更新(PlanUpdate、Update)
	// 把库里的技能换成这一版,之后这一处和库里的当前版本一致,SkillID 就是它。SkillID 不为 0 时为 0。
	SourceLatestOf SkillID `json:"source_latest_of,omitzero"`
}

// ListUnmanagedSkills 列出各作用域(每个 Agent 的全局,以及每个 Agent 在每个已登记项目里)的技能目录里
// 不是库装的技能文件夹,和 ListSkillScopes 列出的库装的技能放在一起,就是 Agent 在这个作用域能看到的技能。
// 技能文件夹是技能目录下直接的子文件夹,或指向文件夹的符号链接,根上有 SKILL.md;以点开头的(如 Codex 的 .system)不算。
// 这个作用域里有安装记录用它的(含共用目录的 Agent 装的,共用按 Agent 的目录设置判断,不跟随符号链接)是库装的,不列出;
// 库装出去后被改过的也不在这里,巡检报为 modified。
// 插件里的技能、企业托管目录、Codex 的 ADMIN / SYSTEM 技能和没有登记的项目都不在范围内。
// 只读:不改库、不改磁盘。Agent 目录不存在、项目不在这台机器上时那个作用域没有结果;几个 Agent 共用一个目录时,
// 每个 Agent 各列一条。结果按 Agent、作用域(全局在前,项目按 ID)、Name 排序。
// 按内容认出它是库里的哪个技能、哪一版(SkillID、VersionID),或是库里哪个技能在来源里现在的那一版(SourceLatestOf);
// 为此只读 SKILL.md 和库里某个版本相同、或技能名和有新版本的库里技能相同的那几个文件夹的全部内容。不联网。
func (l *Library) ListUnmanagedSkills(ctx context.Context) (skills []UnmanagedSkill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	found, err := l.unmanagedSkills(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := l.recognizeUnmanaged(ctx, found); err != nil {
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
	within := agentdir.Within(directories)
	var entries []unmanagedEntry
	for _, found := range folders {
		if found.skillID != 0 {
			continue
		}
		target := found.target
		entry := unmanagedEntry{UnmanagedSkill: UnmanagedSkill{Scope: target.scope(), Name: found.Name, Folder: found.Path, LinkTarget: found.Target}, found: found}
		entry.LinkOutside = found.Target != "" && !within(found.Target)
		if candidate, err := discovery.ParseSkillMD(found.SkillMD, found.Name); err == nil {
			entry.Description = candidate.Description
		}
		if record, ok := records.find(target, skillName(found.Folder), found.Name); ok {
			entry.SourceInput, entry.SourceRef = record.input, record.ref
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
	var paths []string
	var indexes []int
	for i, entry := range entries {
		if !validFolderName(entry.Name) {
			continue
		}
		if !manifests[skillfile.Fingerprint(entry.found.SkillMD)] && !names[skillName(entry.found.Folder)] {
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
		found, err := recognize(ctx, l.store, diskOf(state), []store.Scope{entry.found.target.record()}, latests)
		if err != nil {
			return err
		}
		entry.recognized(found)
		return nil
	})
}

func (u *UnmanagedSkill) recognized(found recognition) {
	u.SkillID, u.VersionID, u.SourceLatestOf = found.skillID, found.versionID, found.sourceLatestOf
	if found.skillID != 0 {
		u.SourceInput, u.SourceRef = "", ""
	}
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
		claimed[folder{skillScope.Scope, agentdir.FolderName(skillScope.Folder)}] = skillScope.SkillID
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

func validFolderName(name string) bool {
	return discovery.ValidName(name) && !strings.HasPrefix(name, ".")
}

func readSkillFolder(ctx context.Context, operation, path, name string) (diskFiles, discovery.Candidate, error) {
	state, err := agentdir.ReadFolder(ctx, path, maxFetchFiles, maxFetchSize)
	if err != nil && state.Err == nil {
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
	// Folders 是 ListUnmanagedSkills 列出的条目,按作用域与文件夹名指定,重复的只算一次。
	// 来源记录已经认出来源(UnmanagedSkill.SourceInput 不为空)的、按内容认出是库里哪个技能的(SkillID 或 SourceLatestOf 不为 0),
	// 和已经不是未纳管技能、不在了的一样,不找也不报错。
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
			found = append(found, FoundUnmanagedSource{Scope: folder.Scope, Name: folder.Name, SourceInput: best.input})
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
	var found []FolderInScope
	var targets []resolvedScope
	var paths []string
	for _, folder := range requested {
		entry, ok := listed[folder]
		if !ok || entry.SourceInput != "" {
			continue
		}
		delete(listed, folder)
		found = append(found, folder)
		targets = append(targets, entry.found.target)
		paths = append(paths, entry.Folder)
	}
	latests, err := l.store.SourceLatests(ctx)
	if err != nil {
		return nil, err
	}
	read := make([]*sourcelessFolder, len(found))
	err = agentdir.ReadFolders(ctx, paths, maxFetchFiles, maxFetchSize, func(i int, state agentdir.FolderState) error {
		disk, parsed, err := parseSkillFolder(operation, state.Path, found[i].Name, state)
		if err != nil {
			return nil
		}
		recognized, err := recognize(ctx, l.store, disk, []store.Scope{targets[i].record()}, latests)
		if err != nil {
			return err
		}
		if recognized == (recognition{}) {
			read[i] = &sourcelessFolder{FolderInScope: found[i], name: parsed.Name, files: disk.files}
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
