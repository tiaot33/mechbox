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
		var readErr error
		if id <= 0 {
			readErr = &Error{Code: InvalidArgument, Message: "skill ID must be positive"}
		} else {
			skill, readErr = l.GetSkill(ctx, id)
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
		completed = append(completed, readErr != nil)
	}
	for _, sourceID := range sources {
		if err := ctx.Err(); err != nil {
			return finish(), err
		}
		indices := groups[sourceID]
		source := skills[indices[0]].Source
		var discovered discoveredSource
		var localFiles []File
		var readErr error
		if source.Type == SourceLocal {
			localFiles, readErr = readFiles(ctx, source.Locator.Path)
		} else {
			discovered, readErr = fetchRemote(ctx, source, "")
		}
		if ctx.Err() != nil {
			return finish(), ctx.Err()
		}
		for _, index := range indices {
			if ctx.Err() != nil {
				return finish(), ctx.Err()
			}
			latest := localFiles
			itemErr := readErr
			if itemErr == nil && source.Type != SourceLocal {
				selected, selectErr := selectSkills(discovered.skills, []string{skills[index].Name})
				itemErr = selectErr
				if itemErr == nil {
					latest = selected[0].files
				}
			}
			if itemErr != nil {
				results[index].Error, _ = errors.AsType[*Error](typedError(itemErr))
			} else {
				results[index].Available = !filesEqual(skills[index].Files, latest)
			}
			completed[index] = true
		}
	}
	return results, ctx.Err()
}
