package library

import (
	"context"
	"errors"
	"path/filepath"
)

func (l *sqliteLibrary) Reinstall(ctx context.Context, filter InstallationFilter, opts ReinstallOptions) (results []ReinstallResult, err error) {
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
	installations, err := l.filteredInstallations(ctx, operation, filter)
	if err != nil {
		return nil, err
	}
	results = []ReinstallResult{}
	for _, installation := range installations {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := ReinstallResult{SkillID: installation.SkillID, Scope: installation.Scope}
		outcome, itemErr := l.reinstallOne(ctx, operation, installation, opts.OnConflict)
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

func (l *sqliteLibrary) reinstallOne(ctx context.Context, operation string, installation Installation, policy ConflictPolicy) (ReinstallOutcome, error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	target, err := l.resolveScope(ctx, operation, installation.Scope)
	if err != nil {
		return "", err
	}
	existing, err := findInstallation(ctx, l.db, installation.SkillID, target.scopeRow)
	if err != nil {
		return "", err
	}
	if existing == nil {
		return "", &Error{Code: NotFound, Message: operation + ": installation no longer exists"}
	}
	skill, _, err := l.getSkill(ctx, installation.SkillID)
	if err != nil {
		return "", err
	}
	if err := target.requireRoot(operation); err != nil {
		return "", err
	}
	status, err := l.inspectInstallation(ctx, Installation{SkillID: skill.ID, Directory: target.directory})
	if err != nil {
		return "", err
	}
	switch status {
	case InstallationCurrent:
		return ReinstallCurrent, nil
	case InstallationModified:
		if policy != Overwrite {
			return "", preconditionError(operation, "installation was modified outside the library", ErrorDetails{Path: filepath.Join(target.directory, skill.Name)})
		}
	}
	if err := l.place(ctx, []*placement{{skill: skill, scope: target}}, nil); err != nil {
		return "", err
	}
	return Reinstalled, nil
}
