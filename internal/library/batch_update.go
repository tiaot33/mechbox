package library

import (
	"context"
	"errors"
	"fmt"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

func (l *sqliteLibrary) CheckUpdates(ctx context.Context, ids []SkillID) (results []UpdateCheck, err error) {
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
		var discovered discoveredSource
		var githubSnap githubSourceSnapshot
		snapshot, readErr := openSnapshot(ctx, src, states[indices[0]].snapshot)
		switch {
		case readErr != nil, snapshot != nil:
		case src.Type == SourceGitHub:
			githubSnap, readErr = l.readGitHubSnapshot(ctx, src)
		default:
			discovered, readErr = l.fetchSource(ctx, src, "", states[indices[0]].httpValidator)
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
				switch {
				case snapshot != nil:
					available, _, itemErr = checkSnapshot(ctx, snapshot, source.Have{Name: skills[index].Name, Files: skills[index].Files, State: states[index].snapshot}, false)
				case src.Type == SourceGitHub:
					available, itemErr = githubSkillHasUpdate(ctx, &githubSnap, skills[index], states[index].confirmedCommit)
				default:
					if discovered.unchanged {
						break
					}
					selected, selectErr := selectSkills(discovered.skills, []string{skills[index].Name})
					itemErr = selectErr
					if itemErr == nil {
						available = !skillfile.Equal(skills[index].Files, selected[0].files)
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

func (l *sqliteLibrary) saveUpdateCheck(ctx context.Context, id SkillID, state sourceState, status UpdateCheckStatus) error {
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
	var storedAt string
	var storedStatus UpdateCheckStatus
	if err := tx.QueryRowContext(ctx, `SELECT update_checked_at, update_check_status FROM skills WHERE id = ?`, id).Scan(&storedAt, &storedStatus); err != nil {
		return fmt.Errorf("read update check time: %w", err)
	}
	if storedAt != state.updateCheckedAt && storedStatus != status && storedStatus != UpdateFailed {
		return &Error{Code: Conflict, Message: "check updates: skill was confirmed while checking", Details: ErrorDetails{SkillID: id}}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET update_checked_at = ?, update_check_status = ? WHERE id = ?`, formatStoredTime(clockNow()), status, id); err != nil {
		return fmt.Errorf("save update check: %w", err)
	}
	return tx.Commit()
}
