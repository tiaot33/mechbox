package library

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/skilllock"
	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

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
	path := agentdir.FolderPath(target.directory, req.Name)
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
		if agentdir.FolderName(skillScope.Folder) == req.Name {
			return unmanagedFolder{}, preconditionError(operation, "the folder is installed by the library", ErrorDetails{Path: path, SkillID: skillScope.SkillID})
		}
	}
	files, parsed, err := readSkillFolder(ctx, operation, path, req.Name)
	if err != nil {
		return unmanagedFolder{}, err
	}
	return unmanagedFolder{target: target, name: req.Name, path: path, files: files, parsed: parsed}, nil
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
			if !entry.recognized || entry.input != input || entry.ref != ref {
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
	home agentdir.Home
	read map[string]skilllock.Records
}

type sourceRecord struct {
	skilllock.Record
	input, ref string
	recognized bool
}

func (l *Library) sourceRecords() *sourceRecords {
	return &sourceRecords{home: l.home, read: map[string]skilllock.Records{}}
}

func (f *sourceRecords) find(target resolvedScope, names ...string) (sourceRecord, bool) {
	root := ""
	if target.projectID != nil {
		root = target.root
	}
	records, ok := f.read[root]
	if !ok {
		if root == "" {
			content, _ := agentdir.ReadFile(f.home.Join(skilllock.GlobalFile))
			records = skilllock.ParseGlobal(content)
		} else {
			content, _ := agentdir.ReadFile(agentdir.Join(root, skilllock.ProjectFile))
			records = skilllock.ParseProject(content)
		}
		f.read[root] = records
	}
	record, ok := records.Find(names...)
	if !ok {
		return sourceRecord{}, false
	}
	found := sourceRecord{Record: record}
	found.input, found.ref, found.recognized = record.Input()
	if path, local := record.LocalPath(); local {
		found.input = agentdir.Resolve(root, path)
		found.recognized = found.input != ""
	}
	return found, true
}
