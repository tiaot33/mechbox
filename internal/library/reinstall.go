package library

import (
	"context"
	"errors"
	"path/filepath"
)

func (l *sqliteLibrary) Reinstall(ctx context.Context, filter SkillScopeFilter, opts ReinstallOptions) (results []ReinstallResult, err error) {
	defer func() { err = typedError(err) }()
	const operation = "reinstall"
	if err := validateConflictPolicy(operation, opts.OnConflict); err != nil {
		return nil, err
	}
	if filter.SkillID == 0 && filter.Scope == nil {
		return nil, argumentError(operation, "filter", "reinstall requires a skill or a scope")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	skillScopes, err := l.filteredSkillScopes(ctx, operation, filter)
	if err != nil {
		return nil, err
	}
	results = []ReinstallResult{}
	for _, skillScope := range skillScopes {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := ReinstallResult{SkillID: skillScope.SkillID, Scope: skillScope.Scope}
		outcome, itemErr := l.reinstallOne(ctx, operation, skillScope, opts.OnConflict)
		if itemErr != nil && ctx.Err() != nil && errors.Is(itemErr, ctx.Err()) {
			return results, ctx.Err()
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

func (l *sqliteLibrary) reinstallOne(ctx context.Context, operation string, skillScope SkillScope, policy ConflictPolicy) (ReinstallOutcome, error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	target, err := l.resolveScope(ctx, operation, skillScope.Scope)
	if err != nil {
		return "", err
	}
	existing, err := l.store.FindSkillScope(ctx, int64(skillScope.SkillID), target.record())
	if err != nil {
		return "", err
	}
	if existing == nil {
		return "", &Error{Code: NotFound, Message: operation + ": skill scope no longer exists"}
	}
	skill, _, err := l.getSkill(ctx, skillScope.SkillID)
	if err != nil {
		return "", err
	}
	if err := target.requireRoot(operation); err != nil {
		return "", err
	}
	status, err := l.inspectSkillScope(ctx, SkillScope{SkillID: skill.ID, Directory: target.directory})
	if err != nil {
		return "", err
	}
	switch status {
	case SkillScopeCurrent:
		return ReinstallCurrent, nil
	case SkillScopeModified:
		if policy != Overwrite {
			return "", preconditionError(operation, "installed folder was modified outside the library", ErrorDetails{Path: filepath.Join(target.directory, skill.Name)})
		}
	}
	if err := l.place(ctx, []*placement{{skill: skill, scope: target}}, nil); err != nil {
		return "", err
	}
	return Reinstalled, nil
}
