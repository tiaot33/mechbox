package library

import (
	"cmp"
	"context"
	"errors"
	"slices"
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
	CheckedAt time.Time         `json:"checked_at"` // 最近一次确认来源的时间:检查更新、更新或跳过
	Status    UpdateCheckStatus `json:"status"`
	// Revision 是这次确认来源时看到的来源版本,与入库预览的 Revision 同义:GitHub 为 commit,URL 为压缩包内容哈希;
	// 本地目录来源与检查失败时为空。
	Revision string `json:"revision,omitempty"`
	// Skipped 为 true 表示有新版本,但来源里这个技能现在的内容,用户之前跳过了(SkipUpdate)。来源里这个技能的内容又变了,
	// 下次检查更新后它回到 false;同一来源里别的技能变了不影响。
	Skipped bool `json:"skipped,omitzero"`
	// Pending 为 true 表示这个技能计入角标,侧边栏的更新计数与 Dock 角标都按它算:有新版本且没跳过,
	// 或来源里已删除且用户没选继续保留(Skill.KeptDeleted)。本地目录来源的技能、跳过的改动、来源里的新技能都不计入。
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

// UpdateRequest 是一次更新:对一个集合发起,带进用户勾选的技能。从哪里发起只决定勾哪些:从集合发起传有新版本的全部技能,
// 从技能详情页发起传 Skill.CollectionID 和这一个技能;单个技能更新就是只勾一项的集合更新。
type UpdateRequest struct {
	CollectionID CollectionID `json:"collection_id"`
	Skills       []SkillID    `json:"skills"` // 勾选的技能,都必须属于这个集合;重复的只算一次。没勾的技能完全不动,检查结论也不变
}

// UpdatedSkill 是更新结果里的一个技能:从哪个版本换到了哪个版本。
type UpdatedSkill struct {
	Skill Skill `json:"skill"` // 更新后的技能;LastUpdateCheck 为 current,时间为这次更新
	// PreviousVersionID 是更新前的当前版本。和 Skill.VersionID 一起传给 DiffVersions 看这次改了什么,
	// 更新前版本的信息用 GetVersion 取。
	PreviousVersionID VersionID `json:"previous_version_id"`
	Changed           bool      `json:"changed"` // 这次更新给技能写入了新版本;为 false 时 PreviousVersionID 等于 Skill.VersionID
}

// UpdateResult 是一次更新的结果:这个集合换成了什么样。
type UpdateResult struct {
	Source Source         `json:"source"` // 集合来自哪里
	Skills []UpdatedSkill `json:"skills"` // 勾选的技能,按请求里的顺序
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
// 同时有几次检查、或检查期间技能被更新时,结论以较新的为准,不报错:检查开始后别处写下了不同的结论(包括更新写下的已是最新)时
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
	skills, states, err := l.collectionCurrent(ctx, id)
	if err != nil {
		return result, err
	}
	if len(skills) == 0 {
		result.Error = &Error{Code: NotFound, Message: "check updates: collection not found"}
		return result, nil
	}
	result.Source = skills[0].Source
	conclusions := make([]store.UpdateCheck, len(skills))
	checked := make([]checkedSkill, len(skills))
	var revision string
	var listed []string
	snapshot, err := l.sources.Open(ctx, states[0].source.Type, states[0].source.Locator, "", states[0].snapshot)
	if err == nil {
		revision = snapshot.Revision()
		checked = checkSkills(ctx, snapshot, haves(skills, states), true)
		listed = l.listSourceSkills(ctx, snapshot, states[0].source.ID, revision)
	} else {
		for i := range checked {
			checked[i].err = err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	for i := range skills {
		itemErr := checked[i].err
		switch {
		case itemErr == nil && checked[i].changed:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateAvailable), Revision: revision, Content: skillfile.Sum(checked[i].latest.Files), Found: true}
		case itemErr == nil:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateCurrent), Revision: revision, Found: true}
		case missingSkill(itemErr):
			conclusions[i] = store.UpdateCheck{Status: string(UpdateDeleted), Revision: revision}
		default:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateFailed)}
			if result.Error == nil {
				result.Error = ErrorOf(itemErr)
			}
		}
	}
	result.Skills, err = l.saveUpdateChecks(ctx, skills, states, conclusions, listed, revision)
	return result, err
}

func (l *Library) listSourceSkills(ctx context.Context, snapshot source.Snapshot, sourceID int64, revision string) []string {
	if revision != "" {
		listedCtx, unlock := l.sharedOperation(ctx)
		listed, err := l.store.ListedRevision(listedCtx, sourceID)
		unlock()
		if err != nil || listed == revision {
			return nil
		}
	}
	names, err := snapshot.Names(ctx)
	if err != nil {
		return nil
	}
	return names
}

func (l *Library) collectionCurrent(ctx context.Context, id CollectionID) ([]Skill, []currentVersion, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	rows, err := l.store.ListCollectionSkills(ctx, int64(id))
	if err != nil {
		return nil, nil, err
	}
	var skills []Skill
	var states []currentVersion
	for _, row := range rows {
		skill, state, err := l.currentOf(ctx, row)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		skills = append(skills, skill)
		states = append(states, state)
	}
	return skills, states, nil
}

func (l *Library) saveUpdateChecks(ctx context.Context, skills []Skill, states []currentVersion, conclusions []store.UpdateCheck, listed []string, revision string) ([]Skill, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	saved := []Skill{}
	err := l.store.InTx(ctx, func(tx *store.Tx) error {
		now := clockNow()
		if listed != nil {
			if err := tx.ListSourceSkills(ctx, states[0].source.ID, listed, revision, false); err != nil {
				return err
			}
		}
		for i, skill := range skills {
			id := int64(skill.ID)
			versionID, err := tx.CurrentVersionID(ctx, id)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			storedAt, storedStatus, err := tx.UpdateCheck(ctx, id)
			if err != nil {
				return err
			}
			stored := UpdateCheckStatus(storedStatus)
			conclusion := conclusions[i]
			newer := versionID != states[i].versionID || !storedAt.Equal(states[i].checkedAt) && stored != UpdateCheckStatus(conclusion.Status) && stored != UpdateFailed
			if !newer {
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

// Update 把用户勾选的技能换成来源现在的内容。不要求先检查更新,更新自己读来源;本地目录来源也用它重新读取。
//
// 只改库,不重新安装:装出去的文件夹保持原样,InspectSkillScopes 会把它们标成 stale,Agent 要用上新版需对各处 Reinstall。
// 勾选的技能都按来源的同一份内容判断,换到同一个来源版本上:内容有变化的写入新版本,没变化的保持当前版本。
// 全成或全不成:任何一个技能失败,库里什么都不变,原有的检查结论也不改。成功后勾选的每个技能的 LastUpdateCheck
// 都是已是最新,时间为这次更新。读取来源期间不妨碍其他操作。
//
// 错误:CollectionID 不是正数、Skills 为空或含不是正数的 ID 返回 InvalidArgument(Details.Field 为 collection_id / skills);
// 勾选的技能不在库里返回 NotFound,不属于这个集合返回 InvalidArgument,都带 Details.SkillID;
// 来源里已没有某个勾选的技能返回 NotFound,带 Details.SkillID 与 Name;读不了来源按来源错误返回
// (SourceUnreachable / Permission / SourceInvalid 等);读取来源期间勾选的技能被移除返回 NotFound,
// 被另一次更新改过返回 Conflict,都带 Details.SkillID。
func (l *Library) Update(ctx context.Context, req UpdateRequest) (result UpdateResult, err error) {
	defer func() { err = typedError(err) }()
	if req.CollectionID <= 0 {
		return UpdateResult{}, argumentError("update", "collection_id", "collection ID must be positive")
	}
	if len(req.Skills) == 0 {
		return UpdateResult{}, argumentError("update", "skills", "at least one skill is required")
	}
	var ids []SkillID
	for _, id := range req.Skills {
		if id <= 0 {
			return UpdateResult{}, argumentError("update", "skills", "skill ID must be positive")
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	skills := make([]Skill, len(ids))
	states := make([]currentVersion, len(ids))
	for i, id := range ids {
		skills[i], states[i], err = l.getCurrent(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return UpdateResult{}, &Error{Code: NotFound, Message: "update: skill not found", Details: ErrorDetails{SkillID: id}, Err: err}
		}
		if err != nil {
			return UpdateResult{}, err
		}
		if skills[i].CollectionID != req.CollectionID {
			return UpdateResult{}, &Error{Code: InvalidArgument, Message: "update: skill is not in this collection", Details: ErrorDetails{Field: "skills", SkillID: id}}
		}
	}
	snapshot, err := l.sources.Open(ctx, states[0].source.Type, states[0].source.Locator, "", states[0].snapshot)
	if err != nil {
		return UpdateResult{}, err
	}
	checked := checkSkills(ctx, snapshot, haves(skills, states), true)
	if err := ctx.Err(); err != nil {
		return UpdateResult{}, err
	}
	for i, skill := range skills {
		if err := checked[i].err; missingSkill(err) {
			return UpdateResult{}, &Error{Code: NotFound, Message: "update: skill not found in source", Details: ErrorDetails{SkillID: skill.ID, Name: skill.Name}, Err: err}
		} else if err != nil {
			return UpdateResult{}, err
		}
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	result = UpdateResult{Source: skills[0].Source, Skills: make([]UpdatedSkill, len(ids))}
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		now := clockNow()
		for i, skill := range skills {
			id := int64(skill.ID)
			if err := requireCurrentVersion(ctx, tx, skill.ID, states[i].versionID); err != nil {
				return err
			}
			revision := cmp.Or(snapshot.Revision(), states[i].revision)
			latest := checked[i].latest
			if checked[i].changed {
				if _, err := tx.AddVersion(ctx, id, store.NewVersion{Description: latest.Description, Files: latest.Files, State: string(latest.State), Revision: revision, CreatedAt: now}); err != nil {
					return err
				}
			} else if err := tx.ConfirmVersion(ctx, id, string(latest.State), revision); err != nil {
				return err
			}
			if err := tx.SaveUpdateCheck(ctx, id, store.UpdateCheck{At: now, Status: string(UpdateCurrent), Revision: snapshot.Revision(), Found: true}); err != nil {
				return err
			}
			row, err := tx.GetSkill(ctx, id)
			if err != nil {
				return err
			}
			updated, err := l.skillOf(row)
			if err != nil {
				return err
			}
			result.Skills[i] = UpdatedSkill{Skill: updated, PreviousVersionID: VersionID(states[i].versionID), Changed: checked[i].changed}
		}
		return nil
	})
	if err != nil {
		return UpdateResult{}, err
	}
	return result, nil
}

// KeepDeletedSkill 记下用户对来源里已删除的这个技能选了继续保留:之后不计入角标,技能上 Skill.KeptDeleted 为 true,重启后仍在。
// 库里的技能和装出去的文件夹都不动;之后检查更新或更新确认来源里又有这个技能时,标记自动失效。返回记下后的技能。
// 技能不存在返回 NotFound;技能最近一次检查更新的结论不是来源里已删除返回 FailedPrecondition。
func (l *Library) KeepDeletedSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		_, status, err := tx.UpdateCheck(ctx, int64(id))
		if err != nil {
			return err
		}
		if UpdateCheckStatus(status) != UpdateDeleted {
			return preconditionError("keep deleted skill", "skill is not deleted from its source", ErrorDetails{SkillID: id})
		}
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

func requireCurrentVersion(ctx context.Context, tx *store.Tx, id SkillID, expected int64) error {
	actual, err := tx.CurrentVersionID(ctx, int64(id))
	if errors.Is(err, store.ErrNotFound) {
		return &Error{Code: NotFound, Message: "skill was removed while fetching", Details: ErrorDetails{SkillID: id}, Err: err}
	}
	if err != nil {
		return err
	}
	if actual != expected {
		return &Error{Code: Conflict, Message: "skill changed while fetching", Details: ErrorDetails{SkillID: id}}
	}
	return nil
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
