package library

import (
	"context"
	"errors"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// UpdateResult 描述 Library 的更新结果;已安装的文件夹不会自动重新安装。
type UpdateResult struct {
	Skill   Skill `json:"skill"`
	Changed bool  `json:"changed"` // GitHub 为目录 tree SHA 变化并生成了新版本;其他来源为内容或权限变化
}

// UpdateCheckStatus 是最近一次更新检查的结论;未检查由 LastUpdateCheck == nil 表达。
type UpdateCheckStatus string

const (
	UpdateAvailable UpdateCheckStatus = "available" // 有更新
	UpdateCurrent   UpdateCheckStatus = "current"   // 无更新
	UpdateFailed    UpdateCheckStatus = "failed"    // 检查失败
)

// UpdateCheckState 随技能保存最近检查的时间与结论,可离线读取。
// 已检查的技能 Update 成功后改为 current,CheckedAt 刷新为此次确认时间。
type UpdateCheckState struct {
	CheckedAt time.Time         `json:"checked_at"`
	Status    UpdateCheckStatus `json:"status"`
}

// UpdateCheck 是一次批量检查中某个 Skill 的结果;Error 非空时 Available 无意义。
type UpdateCheck struct {
	SkillID   SkillID `json:"skill_id"`
	Available bool    `json:"available"`
	Error     *Error  `json:"error,omitempty"`
}

// Update 重新读取来源,在其中按名字重新定位该 skill,不经入库预览。GitHub 先解析一次 commit:与当前版本记录的确认 commit 相同即无变化,不取 tree;
// 否则发现候选、比较该 Skill 目录的 tree SHA:相同则不取辅助文件、不新增版本,不同才取文件并写入新版本
// (带新 SHA 与本次 commit)。无变化时刷新当前版本的确认 commit 与 last_fetched / last_seen_ref;
// 无 SHA 的旧 GitHub 记录回退为比较文件与可执行位,成功后补写 SHA 与确认 commit。
// URL 在当前版本记录了 HTTP validator 时带条件头:304 即无变化,不下载、不新增版本,只刷新 last_fetched;
// 200 则整包比较文件,把新 validator 记到新版本或当前版本上,并写回 last_seen_ref。本地目录重新发现候选后比较文件。
// 只更新 Library,不重新安装已安装的文件夹。
// 联网期间不持库锁;提交前复核:skill 已被移除返回 NotFound,当前版本已被别处改写返回 Conflict
// (Details.SkillID),两者都不留下任何写入。
// 成功后已有 LastUpdateCheck 改为 current 并刷新确认时间;失败保留原记录,从未检查的仍为 nil。
func (l *Library) Update(ctx context.Context, id SkillID) (result UpdateResult, err error) {
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return UpdateResult{}, &Error{Code: InvalidArgument, Message: "update: skill ID must be positive"}
	}
	skill, state, err := l.getSkill(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	snapshot, err := l.sources.Open(ctx, state.source.Type, state.source.Locator, "", state.snapshot)
	if err != nil {
		return UpdateResult{}, err
	}
	changed, latest, err := checkSnapshot(ctx, snapshot, source.Have{Name: skill.Name, Files: internalFiles(skill.Files), State: state.snapshot}, true)
	if err != nil {
		return UpdateResult{}, err
	}
	if changed {
		skill.Files, skill.Description = publicFiles(latest.Files), latest.Description
	}
	if revision := snapshot.Revision(); revision != "" {
		skill.Source.LastSeenRef = revision
	}
	skill.Source.LastFetched = time.Now().UTC()
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := requireCurrentVersion(ctx, tx, id, state.versionID); err != nil {
			return err
		}
		if changed {
			createdAt := clockNow()
			versionID, err := tx.AddVersion(ctx, int64(id), store.NewVersion{Description: skill.Description, Files: latest.Files, State: string(latest.State), CreatedAt: createdAt})
			if err != nil {
				return err
			}
			skill.VersionID = VersionID(versionID)
			skill.UpdatedAt = createdAt
		} else if err := tx.ConfirmVersion(ctx, int64(id), string(latest.State)); err != nil {
			return err
		}
		if err := tx.RecordSourceFetch(ctx, int64(skill.Source.ID), skill.Source.LastFetched, skill.Source.LastSeenRef); err != nil {
			return err
		}
		return tx.ConfirmUpdateCheck(ctx, int64(id), clockNow(), string(UpdateCurrent))
	})
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Skill: skill, Changed: changed}, nil
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

// CheckUpdates 检查指定技能是否有更新;单项检查传入仅含一个 SkillID 的列表。
// 去重保序,每个 Source(含 ref)只远程读取一次:GitHub 解析一次 commit,确认 commit 命中的 Skill
// 直接判定无更新;有未命中的才共用一次 tree,发现结果在同一 Source 内复用,各 Skill 用各自目录 SHA 比较。
// URL 整组只发一次请求,用第一项当前版本记录的 HTTP validator 带条件头;304 时记录同一 validator 的 Skill 判定无更新,
// 记录不同的再无条件读取一次。
// GitHub 无 SHA 的旧记录、本地来源以及 URL 的完整响应,比较文件内容与可执行位。
// 技能不存在或来源读取失败等逐项错误放在 UpdateCheck.Error,不阻断其他来源。
// 每项完成后保存检查时间与结论,经 SkillSummary.LastUpdateCheck 读取;不改技能内容、版本或安装。
// 保存前复核当前版本和确认时间;版本变化或期间确认了不同结论返回 Conflict,被移除返回 NotFound。
// 同版本的并发检查结论相同时仍成功并刷新时间;期间写入的 failed 可被成功结论覆盖,冲突项不覆盖已有记录。
// 整体取消返回已完成项与 context 错误,未完成项保留原记录。持久化故障作为整体错误返回。
func (l *Library) CheckUpdates(ctx context.Context, ids []SkillID) (results []UpdateCheck, err error) {
	defer func() { err = typedError(err) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var skills []Skill
	var states []sourceState
	var completed []bool
	groups := make(map[SourceID][]int)
	var sources []SourceID
	seen := make(map[SkillID]bool)
	finish := func() []UpdateCheck {
		done := make([]UpdateCheck, 0, len(results))
		for i, result := range results {
			if completed[i] {
				done = append(done, result)
			}
		}
		return done
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return finish(), err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		result := UpdateCheck{SkillID: id}
		var skill Skill
		var state sourceState
		var readErr error
		if id <= 0 {
			readErr = &Error{Code: InvalidArgument, Message: "skill ID must be positive"}
		} else {
			skill, state, readErr = l.getSkill(ctx, id)
		}
		if ctx.Err() != nil {
			return finish(), ctx.Err()
		}
		if readErr != nil {
			result.Error, _ = errors.AsType[*Error](typedError(readErr))
		} else {
			if len(groups[skill.Source.ID]) == 0 {
				sources = append(sources, skill.Source.ID)
			}
			groups[skill.Source.ID] = append(groups[skill.Source.ID], len(results))
		}
		results = append(results, result)
		skills = append(skills, skill)
		states = append(states, state)
		completed = append(completed, readErr != nil)
	}
	for _, sourceID := range sources {
		if err := ctx.Err(); err != nil {
			return finish(), err
		}
		indices := groups[sourceID]
		first := states[indices[0]]
		snapshot, readErr := l.sources.Open(ctx, first.source.Type, first.source.Locator, "", first.snapshot)
		if ctx.Err() != nil {
			return finish(), ctx.Err()
		}
		for _, index := range indices {
			if ctx.Err() != nil {
				return finish(), ctx.Err()
			}
			itemErr := readErr
			available := false
			if itemErr == nil {
				available, _, itemErr = checkSnapshot(ctx, snapshot, source.Have{Name: skills[index].Name, Files: internalFiles(skills[index].Files), State: states[index].snapshot}, false)
			}
			if ctx.Err() != nil {
				return finish(), ctx.Err()
			}
			status := UpdateCurrent
			if itemErr != nil {
				status = UpdateFailed
			} else if available {
				status = UpdateAvailable
			}
			if saveErr := l.saveUpdateCheck(ctx, skills[index].ID, states[index], status); saveErr != nil {
				if !IsCode(saveErr, Conflict) && !IsCode(saveErr, NotFound) {
					return finish(), saveErr
				}
				itemErr = saveErr
			}
			if itemErr != nil {
				results[index].Error, _ = errors.AsType[*Error](typedError(itemErr))
			} else {
				results[index].Available = available
			}
			completed[index] = true
		}
	}
	return results, ctx.Err()
}

func (l *Library) saveUpdateCheck(ctx context.Context, id SkillID, state sourceState, status UpdateCheckStatus) error {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := requireCurrentVersion(ctx, tx, id, state.versionID); err != nil {
			return err
		}
		storedAt, storedStatus, err := tx.UpdateCheck(ctx, int64(id))
		if err != nil {
			return err
		}
		if !storedAt.Equal(state.checkedAt) && UpdateCheckStatus(storedStatus) != status && UpdateCheckStatus(storedStatus) != UpdateFailed {
			return &Error{Code: Conflict, Message: "check updates: skill was confirmed while checking", Details: ErrorDetails{SkillID: id}}
		}
		return tx.SaveUpdateCheck(ctx, int64(id), clockNow(), string(status))
	})
}
