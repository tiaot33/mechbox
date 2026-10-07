package library

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
)

// SkillScopeFilter 按技能、作用域缩小 SkillScope 范围,两个条件都可选、可组合;供 Reinstall 与
// InspectSkillScopes 共用。SkillID 为零值表示不按技能筛选,Scope 为 nil 表示不按作用域筛选。
// 未知技能、未知 Agent 或未登记项目返回 NotFound,Scope 组合不合法返回 InvalidArgument;
// 已知但没有安装时是空结果。结果顺序与 ListSkillScopes 一致。
type SkillScopeFilter struct {
	SkillID SkillID `json:"skill_id,omitzero"`
	Scope   *Scope  `json:"scope,omitempty"`
}

// ReinstallOptions 控制重新安装。OnConflict 决定被手动改过(modified)的文件夹:默认 Fail 该条失败,Overwrite 覆盖。
type ReinstallOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

// ReinstallOutcome 是重新安装时一个 SkillScope 的结果。
type ReinstallOutcome string

const (
	Reinstalled      ReinstallOutcome = "reinstalled" // 已按当前版本重新安装(含重新创建丢失的文件夹)
	ReinstallCurrent ReinstallOutcome = "current"     // 已是当前版本,不写盘
	ReinstallFailed  ReinstallOutcome = "failed"      // 未重新安装,原因见 Error;磁盘保持原样
)

// ReinstallResult 是一个 SkillScope 的重新安装结果,形状与 GroupMemberResult 对齐。Error 仅在 ReinstallFailed 时非空:
// FailedPrecondition 为被手动改过且未选 Overwrite(Details.Path 为技能文件夹路径),或作用域根目录不存在
// (Details.Path 为根目录,项目带 ProjectID);NotFound 为执行到该条时该技能已不在此 Scope;其余为 I/O 等故障。
type ReinstallResult struct {
	SkillID SkillID          `json:"skill_id"`
	Scope   Scope            `json:"scope"`
	Outcome ReinstallOutcome `json:"outcome"`
	Error   *Error           `json:"error,omitempty"`
}

// SkillScopeStatus 是一个 SkillScope 对照磁盘后的状态。判定按此顺序,前一条命中即停:
// Scope 目录不存在 → directory_missing;目标文件夹不存在 → missing;
// 磁盘文件集 == 当前版本 → current;磁盘文件集 == 某个非当前版本(按 id 倒序) → stale;
// 否则 modified(含非常规文件或空目录以外的异常)。
type SkillScopeStatus string

const (
	SkillScopeCurrent          SkillScopeStatus = "current"           // 磁盘文件集 == 当前版本
	SkillScopeStale            SkillScopeStatus = "stale"             // 磁盘文件集 == 某个非当前版本
	SkillScopeModified         SkillScopeStatus = "modified"          // 目标文件夹存在,但与任何版本都不一致
	SkillScopeMissing          SkillScopeStatus = "missing"           // Scope 目录存在,目标文件夹不存在
	SkillScopeDirectoryMissing SkillScopeStatus = "directory_missing" // Scope 目录本身不存在
)

// SkillScopeInspection 是一个 SkillScope 及其对照磁盘后的状态。
type SkillScopeInspection struct {
	SkillScope
	Status SkillScopeStatus `json:"status"`
}

func (l *Library) filteredSkillScopes(ctx context.Context, operation string, filter SkillScopeFilter) ([]SkillScope, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	var scope *scopeRow
	if filter.Scope != nil {
		target, err := l.resolveScope(ctx, operation, *filter.Scope)
		if err != nil {
			return nil, err
		}
		scope = &target.scopeRow
	}
	if filter.SkillID != 0 {
		if _, err := l.getSummary(ctx, filter.SkillID); err != nil {
			return nil, err
		}
	}
	return l.listSkillScopes(ctx, filter.SkillID, scope)
}

// InspectSkillScopes 对照磁盘给出 filter 范围内每个 SkillScope 的状态;不给条件时巡检整库。只读:不改库、
// 不改磁盘、不创建目录。结果顺序与 ListSkillScopes 相同,每条内嵌完整 SkillScope,是整库巡检结果的
// 子集且逐条一致。判定规则见 SkillScopeStatus,筛选规则见 SkillScopeFilter。
func (l *Library) InspectSkillScopes(ctx context.Context, filter SkillScopeFilter) (inspections []SkillScopeInspection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	skillScopes, err := l.filteredSkillScopes(ctx, "inspect skill scopes", filter)
	if err != nil {
		return nil, err
	}
	inspections = []SkillScopeInspection{}
	for _, skillScope := range skillScopes {
		status, err := l.inspectSkillScope(ctx, skillScope)
		if err != nil {
			return nil, err
		}
		inspections = append(inspections, SkillScopeInspection{SkillScope: skillScope, Status: status})
	}
	return inspections, nil
}

func (l *Library) inspectSkillScope(ctx context.Context, skillScope SkillScope) (SkillScopeStatus, error) {
	name, currentVersionID, err := l.store.SkillHead(ctx, int64(skillScope.SkillID))
	if err != nil {
		return "", err
	}

	if exists, err := agentdir.Exists(skillScope.Directory); err != nil {
		return "", fmt.Errorf("inspect agent directory: %w", err)
	} else if !exists {
		return SkillScopeDirectoryMissing, nil
	}

	target := filepath.Join(skillScope.Directory, name)
	if exists, err := agentdir.Exists(target); err != nil {
		return "", fmt.Errorf("inspect target folder: %w", err)
	} else if !exists {
		return SkillScopeMissing, nil
	}

	disk, err := agentdir.Read(ctx, target)
	if err != nil {
		if errors.Is(err, agentdir.ErrIrregularFile) {
			return SkillScopeModified, nil
		}
		return "", err
	}

	current, err := l.store.VersionDigests(ctx, currentVersionID)
	if err != nil {
		return "", err
	}
	if maps.Equal(disk, current) {
		return SkillScopeCurrent, nil
	}

	versionIDs, err := l.store.OlderVersionIDs(ctx, int64(skillScope.SkillID), currentVersionID)
	if err != nil {
		return "", err
	}
	for _, versionID := range versionIDs {
		files, err := l.store.VersionDigests(ctx, versionID)
		if err != nil {
			return "", err
		}
		if maps.Equal(disk, files) {
			return SkillScopeStale, nil
		}
	}
	return SkillScopeModified, nil
}

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
