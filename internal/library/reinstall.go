package library

import (
	"context"
	"errors"
	"path/filepath"
)

func (l *sqliteLibrary) Reinstall(ctx context.Context, filter InstallationFilter, opts ReinstallOptions) (results []ReinstallResult, err error) {
	// 逐条各持独占锁,条与条之间释放,批量重新安装期间列表读取不被整批挡住。
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
		// 照 CheckUpdates:取消时返回已完成的各条,未执行的不出现在结果里。
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := ReinstallResult{SkillID: installation.SkillID, Scope: installation.Scope}
		outcome, itemErr := l.reinstallOne(ctx, operation, installation, opts.OnConflict)
		if itemErr != nil && ctx.Err() != nil && errors.Is(itemErr, ctx.Err()) {
			// 这一条因取消已复原,不算完成;取消前已判定的失败(如 modified)照常计入结果。
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

// reinstallOne 在独占锁内把一个安装项带到技能的当前版本,不碰任何安装记录;返回的错误即该条失败的原因,
// 此时磁盘保持原样。列出与执行之间锁已释放,因此重新解析目录并确认记录仍在。
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
	// 根目录不存在时不重建 directory_missing 的安装项,也不创建任何目录。
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
	// 与安装共用写盘路径(暂存 + 原子替换 + 失败复原),只是不写任何记录。
	if err := l.place(ctx, []*placement{{skill: skill, scope: target}}, nil); err != nil {
		return "", err
	}
	return Reinstalled, nil
}
