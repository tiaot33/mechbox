package library

import (
	"context"
	"errors"
)

func (l *sqliteLibrary) CheckUpdates(ctx context.Context, ids []SkillID) (results []UpdateCheck, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
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
		var localFiles []File
		var readErr error
		switch source.Type {
		case SourceLocal:
			localFiles, readErr = readFiles(ctx, source.Locator.Path)
		case SourceGitHub:
			githubSnap, readErr = l.readGitHubSnapshot(ctx, source)
		default:
			// validator 是 Source 级的,同组各 Skill 读到的相同;一次带条件头的请求覆盖整组,304 即全组无更新。
			discovered, readErr = l.fetchRemote(ctx, source, "", states[indices[0]].httpValidator)
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
				case SourceLocal:
					available = !filesEqual(skills[index].Files, localFiles)
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
