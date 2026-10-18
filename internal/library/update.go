package library

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"slices"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// UpdateRequest 是一次更新:按一份更新计划,带上用户勾选的技能和勾选的地方。从哪里发起只决定默认勾哪些:从集合发起勾计划里
// 有变化的全部技能,从技能详情页发起只勾这一个;单个技能更新就是只勾一项的集合更新。本地目录来源的「重新读取」、
// 用目录里的修改更新(计划来自 PlanUpdateFromScope)也是它。
type UpdateRequest struct {
	Plan UpdatePlanID `json:"plan"`
	// Skills 是勾选的技能,都必须在计划里有变化或没变化(来源里已删除的不能勾);重复的只算一次。
	// 有变化而没勾的技能完全不动。
	Skills []SkillID `json:"skills"`
	// Installs 是勾选的地方:勾选的技能装在哪里、换成新版。通常取自 UpdatePlan.Installs;用户改过(modified)的地方,
	// Overwrite 为 true 才覆盖,也就是勾选即同意覆盖。没勾的地方保持原样。用目录里的修改更新时不能列取内容的那个文件夹。
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

// Update 按更新计划把勾选的技能换成来源现在的内容,再把勾选的地方逐处重新安装成新版,一次确认同时完成。
//
// 确认时重新读来源:勾选的技能在来源里的内容和计划里一样,就换到来源现在的来源版本(同一来源里只有别的技能有新提交不算变化);
// 不一样,或者库里这些技能在计划生成后被改过,返回 Conflict(Details.SkillID),让用户重新看计划。集合里在来源里没变化的技能
// 每次都一起确认到这次的来源版本(不产生新版本),所以整个集合更新完显示在同一个来源版本上;有变化而没勾的技能不动。
//
// 先换库:新版本、确认没变化的技能和更新记录一次写入,全成或全不成;之后勾选技能的 LastUpdateCheck 都是已是最新。
// 再逐处重新安装勾选的地方:某一处失败不影响库和其他地方,结果里写明每一处换上没有、没换上的原因(含义同 Reinstall),
// 也记在更新记录上;没换上的地方之后可以用 RetryUpdateInstalls 单独重试。成功后计划句柄失效。
// 是符号链接的地方写成独立的文件夹;没勾的地方链着勾选的地方时,先换上独立的一份、保持原来的内容,不跟着换新版,
// 也不记进更新记录(见 SkillScopeInspection.LinkTarget、LinkedBy)。
// 读来源期间不妨碍其他操作;期间整个库被同步拉回替换时放弃写回,返回 Conflict。
//
// 计划来自 PlanUpdateFromScope 时,确认时重新读那一处的文件夹:内容和计划里一样,就把它作为技能的新版本写进库(沿用当时当前版本的
// 来源版本,SkillVersion.BasedOn 指向它),在集合的历史里记一次更新(UpdateRecord.FromScope 为那一处),再逐处重新安装勾选的地方;
// 不改检查更新的结论。那里的内容或库里的当前版本在计划生成后变了返回 Conflict(Details.SkillID);那一处已不再是被改过的,
// 或读不了,错误同 PlanUpdateFromScope;Installs 列了取内容的那个文件夹(含共用它的 Agent,以及解析符号链接后是同一个真实文件夹的地方)返回 InvalidArgument(Details.Field 为 installs)。
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
	if state.from != nil {
		result, versions, err := l.applyScopeUpdate(ctx, operation, state, ids[0], req.Installs)
		if err != nil {
			return UpdateResult{}, err
		}
		return l.finishUpdate(ctx, operation, req, state, result, versions)
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
	return l.finishUpdate(ctx, operation, req, state, result, versions)
}

func (l *Library) finishUpdate(ctx context.Context, operation string, req UpdateRequest, state *planState, result UpdateResult, versions map[SkillID]int64) (UpdateResult, error) {
	l.previewMu.Lock()
	if l.plans[req.Plan] == state {
		state.timer.Stop()
		delete(l.plans, req.Plan)
	}
	l.previewMu.Unlock()
	var err error
	result.Installs, err = l.reinstallForRecord(ctx, operation, result.Record, state.replica, versions, req.Installs)
	return result, err
}

func (l *Library) applyScopeUpdate(ctx context.Context, operation string, state *planState, id SkillID, installs []ReinstallRequest) (UpdateResult, map[SkillID]int64, error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	item := state.skills[id]
	edit, err := l.readScopeEdit(ctx, operation, id, *state.from)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	if edit.current.versionID != item.versionID || skillfile.Sum(edit.latest.Files) != item.content {
		return UpdateResult{}, nil, &Error{Code: Conflict, Message: operation + ": skill changed since the update plan", Details: ErrorDetails{SkillID: id}}
	}
	for _, install := range installs {
		if l.sameFolder(ctx, operation, id, install.Scope, edit) {
			return UpdateResult{}, nil, &Error{Code: InvalidArgument, Message: operation + ": the update takes its content from this folder", Details: ErrorDetails{Field: "installs", SkillID: id}}
		}
	}
	result := UpdateResult{Source: edit.skill.Source, Skills: make([]UpdatedSkill, 1)}
	versions := map[SkillID]int64{}
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.RequireReplica(ctx, state.replica); err != nil {
			return err
		}
		now := clockNow()
		versionID, err := tx.AddVersion(ctx, int64(id), store.NewVersion{Description: edit.latest.Description, Files: edit.latest.Files, Revision: edit.current.revision, Base: edit.current.versionID, CreatedAt: now})
		if err != nil {
			return err
		}
		from := edit.target.record()
		recordID, err := tx.AddRecord(ctx, store.Record{SourceID: state.sourceID, Kind: recordUpdate, FromScope: &from, At: now, Skills: []store.RecordSkill{{SkillID: int64(id), Name: edit.skill.Name, From: edit.current.versionID, To: versionID}}})
		if err != nil {
			return err
		}
		row, err := tx.GetSkill(ctx, int64(id))
		if err != nil {
			return err
		}
		updated, err := l.skillOf(row)
		if err != nil {
			return err
		}
		result.Record = UpdateRecordID(recordID)
		result.Skills[0] = UpdatedSkill{Skill: updated, PreviousVersionID: VersionID(edit.current.versionID), Changed: true}
		versions[id] = versionID
		return nil
	})
	if err != nil {
		return UpdateResult{}, nil, err
	}
	return result, versions, nil
}

func (l *Library) sameFolder(ctx context.Context, operation string, id SkillID, scope Scope, edit scopeEdit) bool {
	target, err := l.resolveScope(ctx, operation, scope)
	if err != nil {
		return false
	}
	if target.sharesFolderWith(edit.target) {
		return true
	}
	place, err := l.store.PlaceSkillScopes(ctx, int64(id), target.record())
	if err != nil || len(place) == 0 {
		return false
	}
	return agentdir.SameFolder(filepath.Join(target.directory, place[0].Folder), edit.folder)
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
					if err := tx.ForgetRenames(ctx, id); err != nil {
						return err
					}
					recorded.Name, recorded.FromName = latest.Name, skill.Name
				}
				record.Skills = append(record.Skills, recorded)
			} else if err := tx.ConfirmVersion(ctx, id, string(latest.State), revision); err != nil {
				return err
			}
			stored, err := tx.UpdateCheck(ctx, id)
			if err != nil {
				return err
			}
			conclusion := store.UpdateCheck{At: now, Status: string(UpdateCurrent), Revision: read.revision, Found: true, Since: math.MaxInt64}
			if selected < 0 {
				conclusion.Since = read.states[i].checkSerial
			}
			if selected >= 0 || stored.Serial == read.states[i].checkSerial {
				if err := tx.SaveUpdateCheck(ctx, id, conclusion); err != nil {
					return err
				}
			}
			if link := read.linked[i]; link.ID != 0 {
				if err := tx.ForgetRename(ctx, link.ID); err != nil {
					return err
				}
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
