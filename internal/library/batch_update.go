package library

import (
	"context"
	"errors"
	"fmt"
)

func (l *sqliteLibrary) CheckUpdates(ctx context.Context, ids []SkillID) (results []UpdateCheck, err error) {
	// 读取和保存各自短锁,网络阶段不持库锁。
	defer func() { err = typedError(err) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var skills []Skill
	var states []fetchState // 与 skills 对齐:各自的抓取记录
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
		var state fetchState
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
		source := skills[indices[0]].Source
		var discovered discoveredSource
		var githubSnap githubSourceSnapshot
		var readErr error
		switch source.Type {
		case SourceGitHub:
			githubSnap, readErr = l.readGitHubSnapshot(ctx, source)
		default:
			// validator 是 Source 级的,同组各 Skill 读到的相同;一次带条件头的请求覆盖整组,304 即全组无更新。
			discovered, readErr = l.fetchSource(ctx, source, "", states[indices[0]].httpValidator)
		}
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
				switch source.Type {
				case SourceGitHub:
					// commit 命中的 Skill 不参与 tree 比较;同一 Source 下首个未命中的 Skill 才让快照取一次 tree。
					available, itemErr = githubSkillHasUpdate(ctx, &githubSnap, skills[index], states[index].confirmedCommit)
				default:
					if discovered.unchanged {
						break // 304:整组沿用库中内容
					}
					selected, selectErr := selectSkills(discovered.skills, []string{skills[index].Name})
					itemErr = selectErr
					if itemErr == nil {
						available = !filesEqual(skills[index].Files, selected[0].files)
					}
				}
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

// saveUpdateCheck 只给仍处于被比较版本的技能保存结果,不让旧检查覆盖更新后的状态。
func (l *sqliteLibrary) saveUpdateCheck(ctx context.Context, id SkillID, state fetchState, status UpdateCheckStatus) error {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update check: %w", err)
	}
	defer tx.Rollback()
	if err := requireCurrentVersion(ctx, tx, id, state.versionID); err != nil {
		return err
	}
	// 无内容变化的 Update 不生成新版本,仍须防止旧请求覆盖它的确认结果。
	var storedAt string
	var storedStatus UpdateCheckStatus
	if err := tx.QueryRowContext(ctx, `SELECT update_checked_at, update_check_status FROM skills WHERE id = ?`, id).Scan(&storedAt, &storedStatus); err != nil {
		return fmt.Errorf("read update check time: %w", err)
	}
	// 结论相同只刷新时间;期间写入的 failed 不算确认,可被成功结论覆盖。其余不同的新结论才算冲突。
	if storedAt != state.updateCheckedAt && storedStatus != status && storedStatus != UpdateFailed {
		return &Error{Code: Conflict, Message: "check updates: skill was confirmed while checking", Details: ErrorDetails{SkillID: id}}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET update_checked_at = ?, update_check_status = ? WHERE id = ?`, formatStoredTime(clockNow()), status, id); err != nil {
		return fmt.Errorf("save update check: %w", err)
	}
	return tx.Commit()
}
