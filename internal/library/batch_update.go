package library

import (
	"context"
	"errors"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

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
		src := skills[indices[0]].Source
		snapshot, readErr := l.openSnapshot(ctx, src, "", states[indices[0]].snapshot)
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
				available, _, itemErr = checkSnapshot(ctx, snapshot, source.Have{Name: skills[index].Name, Files: skills[index].Files, State: states[index].snapshot}, false)
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
