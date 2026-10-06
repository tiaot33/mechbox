package library

import (
	"context"
	"errors"
	"path/filepath"
)

// Reinstall 把 filter 范围内已有记录的技能文件夹改回技能的当前版本,不改变留着它们的直接安装与组安装,
// SkillScope.CreatedAt 不变。两个条件都不给返回 InvalidArgument,避免误重新安装整库。
// 逐条执行、逐条返回结果(顺序同 ListSkillScopes):已是当前版本的不写盘;stale 原子替换;missing /
// directory_missing 重新创建(可建 Agent 目录,作用域根目录不存在时该条失败且不创建任何目录);
// 被手动改过的默认该条失败,opts.OnConflict 为 Overwrite 时覆盖。每条各自原子、单独持独占锁,
// 一条失败不影响其他条。组安装时被跳过的成员没有安装记录,不在结果里(补齐用 InstallGroup)。
// 整体取消时返回已完成的各条与 Canceled 错误,未执行的不出现在结果里。
func (l *Library) Reinstall(ctx context.Context, filter SkillScopeFilter, opts ReinstallOptions) (results []ReinstallResult, err error) {
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

func (l *Library) reinstallOne(ctx context.Context, operation string, skillScope SkillScope, policy ConflictPolicy) (ReinstallOutcome, error) {
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
