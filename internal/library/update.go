package library

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// UpdateCheckStatus 是检查更新的结论,随技能保存在 Skill.LastUpdateCheck 上。
type UpdateCheckStatus string

const (
	UpdateAvailable UpdateCheckStatus = "available" // 有新版本
	UpdateCurrent   UpdateCheckStatus = "current"   // 已是最新;更新成功后也是它
	// UpdateDeleted 是「来源里已删除」:来源读到了,但里面已经没有这个名字的技能(作者删掉或改了名)。
	// 库里的技能和装出去的文件夹都不动,要不要移除由用户决定。来源整个读不到时不算这种,算 UpdateFailed。
	UpdateDeleted UpdateCheckStatus = "deleted"
	UpdateFailed  UpdateCheckStatus = "failed" // 检查失败:这次没读成来源,可重试;原因只在当场的 CollectionUpdateCheck.Error 里,不保存
)

// UpdateCheck 是随技能保存的检查更新结论,重启后仍可读到。
type UpdateCheck struct {
	CheckedAt time.Time         `json:"checked_at"` // 最近一次确认来源的时间:检查更新、更新计划、更新或回滚
	Status    UpdateCheckStatus `json:"status"`
	// Revision 是这次确认来源时看到的来源版本,与入库预览的 Revision 同义:GitHub 为 commit,URL 为压缩包内容哈希;
	// 本地目录来源与检查失败时为空。
	Revision string `json:"revision,omitempty"`
	// Skipped 为 true 表示有新版本(或推测出了改名),但来源里这份内容用户之前跳过了(SkipUpdate)。来源里这个技能的内容又变了,
	// 下次检查更新后它回到 false;同一来源里别的技能变了不影响。
	Skipped bool `json:"skipped,omitzero"`
	// RenamedTo 是结论为来源里已删除时,推测这个技能在来源里改名成的新名字:同一来源里它消失了,同时出现了一个用户没见过、
	// 内容大部分相同的技能。用户在更新计划里确认(勾上更新)、否认(RejectRename)或跳过(SkipUpdate);没有推测时为空。
	RenamedTo string `json:"renamed_to,omitempty"`
	// Pending 为 true 表示这个技能计入角标,侧边栏的更新计数与 Dock 角标都按它算:有新版本且没跳过,
	// 或来源里已删除且用户没选继续保留(Skill.KeptDeleted),或推测出了改名(RenamedTo)且没跳过。本地目录来源的技能、跳过的改动、
	// 来源里的新技能都不计入。
	Pending bool `json:"pending,omitzero"`
}

// CollectionUpdateCheck 是检查更新时一个集合的结果。
type CollectionUpdateCheck struct {
	CollectionID CollectionID `json:"collection_id"`
	Source       Source       `json:"source,omitzero"` // 集合来自哪里;集合不存在时为零值
	// Skills 是检查后集合里的技能,按 ID 升序;各自的 LastUpdateCheck 就是这次的结论,
	// 检查期间别处写下了较新的结论时给出较新的。检查期间被移除的技能不出现。
	Skills []Skill `json:"skills,omitempty"`
	// Error 是这次没读成来源的原因(连不上、没权限、分支已不存在、来源内容不可用),结论为 failed 的技能就是因此失败。
	// 集合不存在时只有它(NotFound),集合 ID 不是正数时为 InvalidArgument。
	Error *Error `json:"error,omitempty"`
}

// UpdateRequest 是一次更新:按一份更新计划,带上用户勾选的技能和勾选的地方。从哪里发起只决定默认勾哪些:从集合发起勾计划里
// 有变化的全部技能,从技能详情页发起只勾这一个;单个技能更新就是只勾一项的集合更新。本地目录来源的「重新读取」也是它。
type UpdateRequest struct {
	Plan UpdatePlanID `json:"plan"`
	// Skills 是勾选的技能,都必须在计划里有变化或没变化(来源里已删除的不能勾);重复的只算一次。
	// 有变化而没勾的技能完全不动。
	Skills []SkillID `json:"skills"`
	// Installs 是勾选的地方:勾选的技能装在哪里、换成新版。通常取自 UpdatePlan.Installs;用户改过(modified)的地方,
	// Overwrite 为 true 才覆盖,也就是勾选即同意覆盖。没勾的地方保持原样。
	Installs []ReinstallRequest `json:"installs,omitempty"`
}

// UpdatedSkill 是更新结果里的一个技能:从哪个版本换到了哪个版本。
type UpdatedSkill struct {
	Skill Skill `json:"skill"` // 更新后的技能;LastUpdateCheck 为 current,时间为这次更新
	// PreviousVersionID 是更新前的当前版本。和 Skill.VersionID 一起传给 DiffVersions 看这次改了什么,
	// 更新前版本的信息用 GetVersion 取。
	PreviousVersionID VersionID `json:"previous_version_id"`
	Changed           bool      `json:"changed"` // 这次更新给技能写入了新版本;为 false 时 PreviousVersionID 等于 Skill.VersionID
}

// UpdateResult 是一次更新的结果:库换成了什么样,以及勾选的地方各自换上没有。
type UpdateResult struct {
	// Record 是这次更新的记录,回滚(Rollback)、单独重试没换上的地方(RetryUpdateInstalls)都用它;
	// 勾选的技能都没变化、库里没有新版本时不留记录,为零值。
	Record   UpdateRecordID `json:"record,omitzero"`
	Source   Source         `json:"source"`             // 集合来自哪里
	Revision string         `json:"revision,omitempty"` // 集合换到的来源版本,和 UpdatePlan.Revision 同义;本地目录来源为空
	Skills   []UpdatedSkill `json:"skills"`             // 勾选的技能,按请求里的顺序
	// Installs 是勾选的每一处的结果,与 UpdateRequest.Installs 一一对应、顺序相同;结果的含义同 Reinstall。
	Installs []ReinstallResult `json:"installs"`
}

// CheckUpdates 检查给定集合有没有新版本;整库检查就是把 ListCollections 的集合 ID 全部传入。
// 重复的集合只检查一次,结果按传入顺序每个集合一项。没有按技能、按组检查的入口,技能详情页的「有新版本」读 Skill.LastUpdateCheck。
//
// 检查只读:不改技能内容和版本,不碰装出去的文件夹,可以在用户没有操作时发起。集合里的技能都按来源的同一份内容判断,
// 结论写在集合里每个技能的 LastUpdateCheck 上,重启后仍可读到:有新版本、已是最新、来源里已删除,
// 或没读成来源时的检查失败(原因在 CollectionUpdateCheck.Error)。结论还带上看到的来源版本、这次的改动是否跳过过,
// 以及是否计入角标(见 UpdateCheck)。来源换了来源版本时,同时按整个来源记下新技能,列在 Collection.NewSkills 上。
// 不同集合互不影响,一个来源连不上不妨碍其他集合。
//
// 同时有几次检查、或检查期间技能被更新时,结论以较新的为准,不报错:检查开始后别处写下了结论(检查、更新计划、更新、继续保留)时
// 不覆盖它,结果里直接给出它;只有别处写下的检查失败会被这次的结论覆盖。
//
// 集合不存在或 ID 不是正数只放进该集合的 Error。取消时返回已检查完的集合和 Canceled,没检查完的集合保留原来的结论;
// 写库失败作为整体错误返回,连同已检查完的集合。
func (l *Library) CheckUpdates(ctx context.Context, collections []CollectionID) (results []CollectionUpdateCheck, err error) {
	defer func() { err = typedError(err) }()
	results = []CollectionUpdateCheck{}
	seen := make(map[CollectionID]bool, len(collections))
	for _, id := range collections {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		result, err := l.checkCollection(ctx, id)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (l *Library) checkCollection(ctx context.Context, id CollectionID) (CollectionUpdateCheck, error) {
	result := CollectionUpdateCheck{CollectionID: id}
	if id <= 0 {
		result.Error = argumentError("check updates", "collections", "collection ID must be positive")
		return result, nil
	}
	read, err := l.readCollection(ctx, id)
	if err != nil {
		return result, err
	}
	if read == nil {
		result.Error = &Error{Code: NotFound, Message: "check updates: collection not found"}
		return result, nil
	}
	result.Source = read.skills[0].Source
	conclusions, failure := read.conclusions()
	result.Error = failure
	result.Skills, err = l.saveCollectionRead(ctx, read, conclusions)
	if errors.Is(err, store.ErrReplaced) {
		result.Skills, result.Error = nil, ErrorOf(err)
		return result, nil
	}
	return result, err
}

type collectionRead struct {
	skills   []Skill
	states   []currentVersion
	replica  string
	listedAt store.Listed
	snapshot source.Snapshot
	openErr  error
	checked  []checkedSkill
	revision string
	listed   []string
	rejected []map[string][]string
	linked   []string
	seen     map[string]bool
	renames  map[int]renameGuess
}

type renameGuess struct {
	latest  source.Skill
	content string
}

func (l *Library) readCollection(ctx context.Context, id CollectionID) (*collectionRead, error) {
	read, err := l.collectionCurrent(ctx, id)
	if err != nil || read == nil {
		return nil, err
	}
	read.checked = make([]checkedSkill, len(read.skills))
	first := read.states[0]
	read.snapshot, read.openErr = l.sources.Open(ctx, first.source.Type, first.source.Locator, "", first.snapshot)
	if read.openErr != nil {
		for i := range read.checked {
			read.checked[i].err = read.openErr
		}
	} else {
		for {
			read.checked = checkSkills(ctx, read.snapshot, haves(read.skills, read.states), true)
			read.revision = read.snapshot.Revision()
			read.listed = nil
			if read.revision == "" || read.revision != read.listedAt.Revision {
				if names, err := read.snapshot.Names(ctx); err == nil {
					read.listed = names
				}
			}
			read.renames = guessRenames(ctx, read)
			if read.snapshot.Revision() == read.revision {
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

func (r *collectionRead) conclusions() ([]store.UpdateCheck, *Error) {
	conclusions := make([]store.UpdateCheck, len(r.skills))
	var failure *Error
	for i, checked := range r.checked {
		switch {
		case checked.err == nil && checked.changed:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateAvailable), Revision: r.revision, Content: skillfile.Sum(checked.latest.Files), Found: true}
		case checked.err == nil:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateCurrent), Revision: r.revision, Found: true}
		case missingSkill(checked.err):
			conclusions[i] = store.UpdateCheck{Status: string(UpdateDeleted), Revision: r.revision, Renamed: r.renames[i].latest.Name, Content: r.renames[i].content}
		default:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateFailed)}
			if failure == nil {
				failure = ErrorOf(checked.err)
			}
		}
	}
	return conclusions, failure
}

func (l *Library) collectionCurrent(ctx context.Context, id CollectionID) (*collectionRead, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	rows, err := l.store.ListCollectionSkills(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	read := &collectionRead{}
	for _, row := range rows {
		skill, state, err := l.currentOf(ctx, row)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rejected, err := l.store.RejectedRenames(ctx, int64(skill.ID))
		if err != nil {
			return nil, err
		}
		linked, err := l.store.RolledBackRename(ctx, int64(skill.ID))
		if err != nil {
			return nil, err
		}
		read.linked = append(read.linked, linked)
		read.skills = append(read.skills, skill)
		read.states = append(read.states, state)
		read.rejected = append(read.rejected, rejected)
	}
	if len(read.skills) == 0 {
		return nil, nil
	}
	if read.replica, err = l.store.Replica(ctx); err != nil {
		return nil, err
	}
	if read.listedAt, err = l.store.Listed(ctx, read.states[0].source.ID); err != nil {
		return nil, err
	}
	known, err := l.store.SourceSkills(ctx, read.states[0].source.Type, read.states[0].source.Locator)
	if err != nil {
		return nil, err
	}
	read.seen = known.Seen
	return read, nil
}

func (l *Library) saveCollectionRead(ctx context.Context, read *collectionRead, conclusions []store.UpdateCheck) ([]Skill, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	saved := []Skill{}
	err := l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.RequireReplica(ctx, read.replica); err != nil {
			return err
		}
		now := clockNow()
		if read.openErr == nil {
			sourceID := read.states[0].source.ID
			listedAt, err := tx.Listed(ctx, sourceID)
			if err != nil {
				return err
			}
			switch {
			case listedAt.Serial != read.listedAt.Serial:
			case read.listed != nil:
				err = tx.ListSourceSkills(ctx, sourceID, read.listed, read.revision, false)
			case read.revision == read.listedAt.Revision:
				err = tx.ConfirmListed(ctx, sourceID)
			}
			if err != nil {
				return err
			}
		}
		for i, skill := range read.skills {
			id := int64(skill.ID)
			current, err := tx.GetSkill(ctx, id)
			if errors.Is(err, store.ErrNotFound) || err == nil && (current.Source.ID != read.states[i].source.ID || current.Name != skill.Name) {
				continue
			}
			if err != nil {
				return err
			}
			versionID := current.VersionID
			stored, err := tx.UpdateCheck(ctx, id)
			if err != nil {
				return err
			}
			conclusion := conclusions[i]
			conclusion.Since = read.states[i].checkSerial
			written := stored.Serial != read.states[i].checkSerial
			if versionID == read.states[i].versionID && (!written || UpdateCheckStatus(stored.Status) == UpdateFailed) {
				conclusion.At = now
				if err := tx.SaveUpdateCheck(ctx, id, conclusion); err != nil {
					return err
				}
			}
			row, err := tx.GetSkill(ctx, id)
			if err != nil {
				return err
			}
			updated, err := l.skillOf(row)
			if err != nil {
				return err
			}
			saved = append(saved, updated)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// Update 按更新计划把勾选的技能换成来源现在的内容,再把勾选的地方逐处重新安装成新版,一次确认同时完成。
//
// 确认时重新读来源:勾选的技能在来源里的内容和计划里一样,就换到来源现在的来源版本(同一来源里只有别的技能有新提交不算变化);
// 不一样,或者库里这些技能在计划生成后被改过,返回 Conflict(Details.SkillID),让用户重新看计划。集合里在来源里没变化的技能
// 每次都一起确认到这次的来源版本(不产生新版本),所以整个集合更新完显示在同一个来源版本上;有变化而没勾的技能不动。
//
// 先换库:新版本、确认没变化的技能和更新记录一次写入,全成或全不成;之后勾选技能的 LastUpdateCheck 都是已是最新。
// 再逐处重新安装勾选的地方:某一处失败不影响库和其他地方,结果里写明每一处换上没有、没换上的原因(含义同 Reinstall),
// 也记在更新记录上;没换上的地方之后可以用 RetryUpdateInstalls 单独重试。成功后计划句柄失效。
// 读来源期间不妨碍其他操作;期间整个库被同步拉回替换时放弃写回,返回 Conflict。
//
// 错误:句柄未知或已失效返回 NotFound;Skills 为空、含不在计划里的技能,Installs 的技能不在 Skills 里、作用域不合法或同一处列了两次,
// 返回 InvalidArgument(Details.Field 为 skills / installs,技能带 Details.SkillID);勾选的技能在来源里已删除返回 NotFound
// (Details.SkillID 与 Name);读取来源期间勾选的技能被移除返回 NotFound(Details.SkillID);读不了来源按来源错误返回。
// 写库之后被取消时库已换好,返回已完成的几处与 Canceled。
func (l *Library) Update(ctx context.Context, req UpdateRequest) (result UpdateResult, err error) {
	defer func() { err = typedError(err) }()
	const operation = "update"
	state, err := l.lookupPlan(operation, req.Plan)
	if err != nil {
		return UpdateResult{}, err
	}
	ids, err := selectedSkills(operation, state, req.Skills)
	if err != nil {
		return UpdateResult{}, err
	}
	if err := validateInstalls(operation, ids, req.Installs); err != nil {
		return UpdateResult{}, err
	}
	read, err := l.readCollection(ctx, state.collection)
	if err != nil {
		return UpdateResult{}, err
	}
	if read == nil {
		return UpdateResult{}, &Error{Code: NotFound, Message: operation + ": skill was removed while fetching", Details: ErrorDetails{SkillID: ids[0]}}
	}
	if read.replica != state.replica {
		return UpdateResult{}, store.ErrReplaced
	}
	if read.openErr != nil {
		return UpdateResult{}, read.openErr
	}
	index := make(map[SkillID]int, len(read.skills))
	for i, skill := range read.skills {
		index[skill.ID] = i
	}
	for _, id := range ids {
		item := state.skills[id]
		i, ok := index[id]
		if !ok || read.skills[i].Name != item.name {
			return UpdateResult{}, &Error{Code: NotFound, Message: operation + ": skill was removed while fetching", Details: ErrorDetails{SkillID: id}}
		}
		checked := read.checked[i]
		if item.renameTo != "" {
			guess, renamed := read.renames[i]
			if !missingSkill(checked.err) || !renamed || guess.latest.Name != item.renameTo || guess.content != item.content || read.states[i].versionID != item.versionID {
				return UpdateResult{}, &Error{Code: Conflict, Message: operation + ": skill changed since the update plan", Details: ErrorDetails{SkillID: id}}
			}
			continue
		}
		if missingSkill(checked.err) {
			return UpdateResult{}, &Error{Code: NotFound, Message: operation + ": skill not found in source", Details: ErrorDetails{SkillID: id, Name: item.name}, Err: checked.err}
		}
		if checked.err != nil {
			return UpdateResult{}, checked.err
		}
		same := !checked.changed
		if item.change == PlannedChanged {
			same = checked.changed && skillfile.Sum(checked.latest.Files) == item.content
		}
		if read.states[i].versionID != item.versionID || !same {
			return UpdateResult{}, &Error{Code: Conflict, Message: operation + ": skill changed since the update plan", Details: ErrorDetails{SkillID: id}}
		}
	}
	commits, err := l.planCommits(ctx, read.snapshot, read.states)
	if err != nil {
		return UpdateResult{}, err
	}
	result, versions, err := l.applyUpdate(ctx, read, ids, commits)
	if err != nil {
		return UpdateResult{}, err
	}
	l.previewMu.Lock()
	if l.plans[req.Plan] == state {
		state.timer.Stop()
		delete(l.plans, req.Plan)
	}
	l.previewMu.Unlock()
	result.Installs, err = l.reinstallForRecord(ctx, operation, result.Record, read.replica, versions, req.Installs)
	return result, err
}

func selectedSkills(operation string, state *planState, requested []SkillID) ([]SkillID, error) {
	if len(requested) == 0 {
		return nil, argumentError(operation, "skills", "at least one skill is required")
	}
	var ids []SkillID
	for _, id := range requested {
		item := state.skills[id]
		if item == nil {
			return nil, &Error{Code: InvalidArgument, Message: operation + ": skill is not in this update plan", Details: ErrorDetails{Field: "skills", SkillID: id}}
		}
		if item.change == PlannedDeleted && item.renameTo == "" {
			return nil, &Error{Code: NotFound, Message: operation + ": skill not found in source", Details: ErrorDetails{SkillID: id, Name: item.name}}
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func validateInstalls(operation string, ids []SkillID, installs []ReinstallRequest) error {
	type place struct {
		skillID SkillID
		scope   Scope
	}
	seen := make(map[place]bool, len(installs))
	for _, install := range installs {
		if _, err := scopeOf(operation, install.Scope); err != nil {
			return err
		}
		if !slices.Contains(ids, install.SkillID) {
			return &Error{Code: InvalidArgument, Message: operation + ": install of a skill this update does not take", Details: ErrorDetails{Field: "installs", SkillID: install.SkillID}}
		}
		key := place{install.SkillID, install.Scope}
		if seen[key] {
			return argumentError(operation, "installs", "the same skill scope is listed more than once")
		}
		seen[key] = true
	}
	return nil
}

func (l *Library) applyUpdate(ctx context.Context, read *collectionRead, ids []SkillID, commits []Commit) (UpdateResult, map[SkillID]int64, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	result := UpdateResult{Source: read.skills[0].Source, Revision: read.revision, Skills: make([]UpdatedSkill, len(ids))}
	versions := map[SkillID]int64{}
	err := l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.RequireReplica(ctx, read.replica); err != nil {
			return err
		}
		now := clockNow()
		record := store.Record{SourceID: read.states[0].source.ID, Kind: recordUpdate, Revision: read.revision, At: now}
		for i, skill := range read.skills {
			checked := read.checked[i]
			selected := slices.Index(ids, skill.ID)
			guess, renamed := read.renames[i]
			renamed = renamed && selected >= 0
			if renamed {
				checked = checkedSkill{changed: true, latest: guess.latest}
			}
			if checked.err != nil || selected < 0 && checked.changed {
				continue
			}
			id := int64(skill.ID)
			current, err := tx.GetSkill(ctx, id)
			if errors.Is(err, store.ErrNotFound) || err == nil && (current.Name != skill.Name || current.VersionID != read.states[i].versionID) {
				if selected < 0 {
					continue
				}
				if errors.Is(err, store.ErrNotFound) {
					return &Error{Code: NotFound, Message: "skill was removed while fetching", Details: ErrorDetails{SkillID: skill.ID}, Err: err}
				}
				return &Error{Code: Conflict, Message: "skill changed while fetching", Details: ErrorDetails{SkillID: skill.ID}}
			}
			if err != nil {
				return err
			}
			revision := cmp.Or(read.revision, read.states[i].revision)
			latest := checked.latest
			if checked.changed {
				versionID, err := tx.AddVersion(ctx, id, store.NewVersion{Description: latest.Description, Files: latest.Files, State: string(latest.State), Revision: revision, CreatedAt: now})
				if err != nil {
					return err
				}
				recorded := store.RecordSkill{SkillID: id, Name: skill.Name, From: read.states[i].versionID, To: versionID}
				if renamed {
					if err := tx.RenameSkill(ctx, id, latest.Name); errors.Is(err, store.ErrAlreadyExists) {
						return &Error{Code: Conflict, Message: "another skill in the collection already has the new name", Details: ErrorDetails{SkillID: skill.ID, Name: latest.Name}, Err: err}
					} else if err != nil {
						return err
					}
					if err := tx.MarkSeen(ctx, read.states[i].source.ID, []string{latest.Name}); err != nil {
						return err
					}
					if err := tx.ForgetRename(ctx, id); err != nil {
						return err
					}
					recorded.Name, recorded.FromName = latest.Name, skill.Name
				}
				record.Skills = append(record.Skills, recorded)
			} else if err := tx.ConfirmVersion(ctx, id, string(latest.State), revision); err != nil {
				return err
			}
			if err := tx.SaveUpdateCheck(ctx, id, store.UpdateCheck{At: now, Status: string(UpdateCurrent), Revision: read.revision, Found: true, Since: math.MaxInt64}); err != nil {
				return err
			}
			if selected < 0 {
				continue
			}
			row, err := tx.GetSkill(ctx, id)
			if err != nil {
				return err
			}
			updated, err := l.skillOf(row)
			if err != nil {
				return err
			}
			versions[skill.ID] = row.VersionID
			result.Skills[selected] = UpdatedSkill{Skill: updated, PreviousVersionID: VersionID(read.states[i].versionID), Changed: checked.changed}
		}
		if len(record.Skills) == 0 {
			return nil
		}
		if len(commits) > 0 {
			encoded, err := json.Marshal(commits)
			if err != nil {
				return err
			}
			record.Commits = string(encoded)
		}
		id, err := tx.AddRecord(ctx, record)
		result.Record = UpdateRecordID(id)
		return err
	})
	if err != nil {
		return UpdateResult{}, nil, err
	}
	return result, versions, nil
}

func (l *Library) reinstallForRecord(ctx context.Context, operation string, record UpdateRecordID, replica string, versions map[SkillID]int64, requests []ReinstallRequest) ([]ReinstallResult, error) {
	results := []ReinstallResult{}
	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := ReinstallResult{SkillID: request.SkillID, Scope: request.Scope}
		outcome, itemErr := l.reinstallOne(ctx, operation, request, &updateInstall{record: record, replica: replica, version: versions[request.SkillID]})
		if itemErr != nil && ctx.Err() != nil && errors.Is(itemErr, ctx.Err()) {
			return results, ctx.Err()
		}
		if errors.Is(itemErr, store.ErrReplaced) {
			return results, itemErr
		}
		if itemErr != nil {
			result.Outcome, result.Error = ReinstallFailed, ErrorOf(itemErr)
		} else {
			result.Outcome = outcome
		}
		results = append(results, result)
	}
	return results, nil
}

// RetryUpdateInstalls 单独重试一次更新里没换上的地方,结果算进那次更新:之后回滚那次更新时,重试换上的地方也一起装回去。
// 每一处换成那次更新带来的版本,结果的含义同 Reinstall,与 installs 一一对应、顺序相同;技能在那次更新之后又换了版本
// (又被更新或回滚过)时这一处返回 Conflict,不动。
// 记录不存在返回 NotFound;不是更新记录、installs 里的技能不是那次更新换了版本的、作用域不合法或同一处列了两次,返回 InvalidArgument;
// 那次更新已被回滚返回 FailedPrecondition。
func (l *Library) RetryUpdateInstalls(ctx context.Context, id UpdateRecordID, installs []ReinstallRequest) (results []ReinstallResult, err error) {
	defer func() { err = typedError(err) }()
	const operation = "retry update installs"
	record, replica, err := l.recordWithReplica(ctx, id)
	if err != nil {
		return nil, err
	}
	if record.Kind != recordUpdate {
		return nil, argumentError(operation, "record", "not an update record")
	}
	if record.RolledBackBy != 0 {
		return nil, preconditionError(operation, "the update was rolled back", ErrorDetails{})
	}
	versions := map[SkillID]int64{}
	var ids []SkillID
	for _, skill := range record.Skills {
		versions[SkillID(skill.SkillID)] = skill.To
		ids = append(ids, SkillID(skill.SkillID))
	}
	if err := validateInstalls(operation, ids, installs); err != nil {
		return nil, err
	}
	return l.reinstallForRecord(ctx, operation, id, replica, versions, installs)
}

// KeepDeletedSkill 记下用户对来源里已删除的这个技能选了继续保留:之后不计入角标,技能上 Skill.KeptDeleted 为 true,重启后仍在。
// 在检查更新的结论或更新计划显示来源里已删除时调用;标记只在结论为来源里已删除时起作用。库里的技能和装出去的文件夹都不动;
// 之后检查更新、更新计划或更新确认来源里又有这个技能时,标记自动失效。返回记下后的技能。技能不存在返回 NotFound。
func (l *Library) KeepDeletedSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.KeepDeleted(ctx, int64(id)); err != nil {
			return err
		}
		row, err := tx.GetSkill(ctx, int64(id))
		if err != nil {
			return err
		}
		skill, err = l.skillOf(row)
		return err
	})
	if err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func missingSkill(err error) bool {
	missing, ok := errors.AsType[*source.NotFoundError](err)
	return ok && missing.What == source.MissingSkill
}

type checkedSkill struct {
	changed bool
	latest  source.Skill
	err     error
}

func haves(skills []Skill, states []currentVersion) []source.Have {
	result := make([]source.Have, len(skills))
	for i, skill := range skills {
		result[i] = source.Have{Name: skill.Name, Files: states[i].files, State: states[i].snapshot}
	}
	return result
}

func checkSkills(ctx context.Context, snapshot source.Snapshot, haves []source.Have, needFiles bool) []checkedSkill {
	for {
		revision := snapshot.Revision()
		results := make([]checkedSkill, len(haves))
		for i, have := range haves {
			if err := ctx.Err(); err != nil {
				results[i].err = err
				continue
			}
			results[i].changed, results[i].latest, results[i].err = checkSnapshot(ctx, snapshot, have, needFiles)
		}
		if snapshot.Revision() == revision {
			return results
		}
	}
}

func checkSnapshot(ctx context.Context, snapshot source.Snapshot, have source.Have, needFiles bool) (bool, source.Skill, error) {
	status, state, err := snapshot.Check(ctx, have)
	if err != nil {
		return false, source.Skill{}, err
	}
	switch {
	case status == source.Unchanged:
		return false, source.Skill{Name: have.Name, Files: have.Files, State: state}, nil
	case status == source.Changed && !needFiles:
		return true, source.Skill{}, nil
	}
	fetched, err := snapshot.Fetch(ctx, []string{have.Name})
	if err != nil {
		return false, source.Skill{}, err
	}
	latest := fetched[0]
	return status == source.Changed || !skillfile.Equal(have.Files, latest.Files), latest, nil
}

const renameSimilarity = 0.6

func guessRenames(ctx context.Context, read *collectionRead) map[int]renameGuess {
	var missing []int
	for i, checked := range read.checked {
		if missingSkill(checked.err) {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	names := read.listed
	if names == nil {
		var err error
		if names, err = read.snapshot.Names(ctx); err != nil {
			return nil
		}
	}
	inLibrary := func(name string) bool {
		return slices.ContainsFunc(read.skills, func(skill Skill) bool { return skill.Name == name })
	}
	var candidates []string
	for _, name := range names {
		linked := slices.ContainsFunc(missing, func(i int) bool { return read.linked[i] == name })
		if (linked || !read.seen[name]) && !inLibrary(name) && !slices.Contains(candidates, name) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	fetched, err := read.snapshot.Fetch(ctx, candidates)
	if err != nil {
		return nil
	}
	type pair struct {
		skill     int
		candidate int
		score     float64
	}
	var pairs []pair
	contents := make([]string, len(fetched))
	for j, candidate := range fetched {
		contents[j] = skillfile.Sum(candidate.Files)
	}
	for _, i := range missing {
		for j, candidate := range fetched {
			if slices.Contains(read.rejected[i][candidate.Name], contents[j]) {
				continue
			}
			if read.linked[i] == candidate.Name {
				pairs = append(pairs, pair{i, j, 2})
			} else if score := similarity(read.states[i].files, candidate.Files); score >= renameSimilarity && !read.seen[candidate.Name] {
				pairs = append(pairs, pair{i, j, score})
			}
		}
	}
	slices.SortStableFunc(pairs, func(a, b pair) int { return cmp.Compare(b.score, a.score) })
	guesses := map[int]renameGuess{}
	used := map[int]bool{}
	for _, p := range pairs {
		if _, done := guesses[p.skill]; done || used[p.candidate] {
			continue
		}
		used[p.candidate] = true
		guesses[p.skill] = renameGuess{latest: fetched[p.candidate], content: contents[p.candidate]}
	}
	return guesses
}

func similarity(old, latest []skillfile.File) float64 {
	byPath := make(map[string]skillfile.File, len(latest))
	for _, file := range latest {
		byPath[file.Path] = file
	}
	var matched float64
	for _, file := range old {
		other, ok := byPath[file.Path]
		if !ok {
			continue
		}
		a, b := file.Content, other.Content
		if file.Path == "SKILL.md" {
			a, b = withoutName(a), withoutName(b)
		}
		if bytes.Equal(a, b) {
			matched++
			continue
		}
		matched += lineOverlap(a, b)
	}
	return matched / float64(max(len(old), len(latest)))
}

func withoutName(content []byte) []byte {
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		return content
	}
	frontmatter, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return content
	}
	var kept [][]byte
	for line := range bytes.SplitSeq(frontmatter, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("name:")) {
			kept = append(kept, line)
		}
	}
	return append(append(bytes.Join(kept, []byte("\n")), '\n'), body...)
}

func lineOverlap(a, b []byte) float64 {
	counts := map[string]int{}
	var total, common int
	for line := range strings.SplitSeq(string(a), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			counts[line]++
			total++
		}
	}
	lines := 0
	for line := range strings.SplitSeq(string(b), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		lines++
		if counts[line] > 0 {
			counts[line]--
			common++
		}
	}
	if total == 0 || lines == 0 {
		return 0
	}
	return float64(common) / float64(max(total, lines))
}
