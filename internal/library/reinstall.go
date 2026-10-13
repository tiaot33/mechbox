package library

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
)

// SkillScopeFilter 按技能、作用域缩小 SkillScope 范围,两个条件都可选、可组合;供 ListSkillScopes 与
// InspectSkillScopes 共用,两者只差读不读磁盘。SkillID 为零值表示不按技能筛选,Scope 为 nil 表示不按作用域筛选,
// 都不给时是全库。未知技能、未知 Agent 或未登记项目返回 NotFound,Scope 组合不合法返回 InvalidArgument;
// 已知但没有安装时是空结果。结果按技能 ID、Agent 排序,全局排在同一 Agent 的项目 Scope 之前。
type SkillScopeFilter struct {
	SkillID SkillID `json:"skill_id,omitzero"`
	Scope   *Scope  `json:"scope,omitempty"`
}

// ReinstallRequest 是要重新安装的一处:某个技能装在某个 Scope。SkillID 与 Scope 可直接取自巡检结果。
type ReinstallRequest struct {
	SkillID SkillID `json:"skill_id"`
	Scope   Scope   `json:"scope"`
	// Overwrite 只对这一处有效:这里被改过(modified)时,同意用库里的当前版本覆盖。默认不覆盖。
	Overwrite bool `json:"overwrite"`
}

// ReinstallOutcome 是重新安装时一处的结果。
type ReinstallOutcome string

const (
	Reinstalled      ReinstallOutcome = "reinstalled" // 换成了当前版本,包括把不在了的文件夹放回去
	ReinstallCurrent ReinstallOutcome = "current"     // 本来就是当前版本,没动
	ReinstallFailed  ReinstallOutcome = "failed"      // 没换上,原因见 Error;这一处保持原样
)

// ReinstallResult 是一处的重新安装结果。Error 仅在 ReinstallFailed 时非空:
//   - AlreadyExists:这里被改过,且没同意覆盖(Details.Path 为技能文件夹);
//   - FailedPrecondition:作用域根目录不存在,即项目不在这台机器上(Details.Path 为根目录,项目带 ProjectID);
//   - NotFound:执行到这一处时技能已不装在这里,或技能、Agent、项目已不在库里;
//   - 其他为权限或 I/O 故障。
type ReinstallResult struct {
	SkillID SkillID          `json:"skill_id"`
	Scope   Scope            `json:"scope"`
	Outcome ReinstallOutcome `json:"outcome"`
	Error   *Error           `json:"error,omitempty"`
}

// SkillScopeStatus 是一处安装现在的状态,每种状态对应界面上的一个动作。判定按下列顺序,前一条命中即停:
// root_missing、missing,然后与库里的版本比对。状态值以后可能增加,调用方遇到不认识的值按「需要用户查看」处理。
type SkillScopeStatus string

const (
	SkillScopeRootMissing SkillScopeStatus = "root_missing" // 作用域根目录不存在,即项目不在这台机器上;重新安装也装不上,先把已登记项目指到本机路径
	SkillScopeMissing     SkillScopeStatus = "missing"      // 技能文件夹不在了(包括整个 Agent 目录不在了);重新安装会把它放回去
	SkillScopeCurrent     SkillScopeStatus = "current"      // 和库里的当前版本完全一致
	SkillScopeStale       SkillScopeStatus = "stale"        // 和库里某个旧版本完全一致;重新安装不用额外确认
	SkillScopeModified    SkillScopeStatus = "modified"     // 和库里任何版本都对不上(包括多了、少了文件,或有不是普通文件的东西),即用户在这里改过;重新安装要明确同意覆盖
)

// SkillScopeInspection 是一处安装及其现在的状态。
type SkillScopeInspection struct {
	SkillScope
	Status SkillScopeStatus `json:"status"`
	// VersionID 是和磁盘上一致的库内版本:current 时是当前版本,stale 时是那个旧版本,其他状态为零值。
	// 可用 GetVersion 查它对应的来源版本。
	VersionID VersionID `json:"version_id,omitzero"`
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
		if _, err := l.GetSkill(ctx, filter.SkillID); err != nil {
			return nil, err
		}
	}
	return l.listSkillScopes(ctx, filter.SkillID, scope)
}

// InspectSkillScopes 对照磁盘给出 filter 范围内每一处安装现在的状态;零值 filter 巡检全库。只读:不改库、
// 不改磁盘、不创建目录。结果与同一 filter 的 ListSkillScopes 逐条对应。技能详情页、项目页据此显示各处状态;
// 移除技能、卸载前的确认框据此说明哪几处用户改过、会丢掉这些修改。状态见 SkillScopeStatus,筛选规则见 SkillScopeFilter。
func (l *Library) InspectSkillScopes(ctx context.Context, filter SkillScopeFilter) (inspections []SkillScopeInspection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "inspect skill scopes"
	skillScopes, err := l.filteredSkillScopes(ctx, operation, filter)
	if err != nil {
		return nil, err
	}
	return l.inspectSkillScopes(ctx, operation, skillScopes)
}

func (l *Library) inspectSkillScopes(ctx context.Context, operation string, skillScopes []SkillScope) ([]SkillScopeInspection, error) {
	inspections := []SkillScopeInspection{}
	for _, skillScope := range skillScopes {
		target, err := l.resolveScope(ctx, operation, skillScope.Scope)
		if err != nil {
			return nil, err
		}
		status, versionID, err := l.inspectSkillScope(ctx, skillScope.SkillID, target)
		if err != nil {
			return nil, err
		}
		inspections = append(inspections, SkillScopeInspection{SkillScope: skillScope, Status: status, VersionID: versionID})
	}
	return inspections, nil
}

func (l *Library) inspectSkillScope(ctx context.Context, id SkillID, target resolvedScope) (SkillScopeStatus, VersionID, error) {
	name, currentVersionID, err := l.store.SkillHead(ctx, int64(id))
	if err != nil {
		return "", 0, err
	}

	if missing, err := target.rootMissing("inspect skill scope"); err != nil {
		return "", 0, err
	} else if missing != nil {
		return SkillScopeRootMissing, 0, nil
	}

	folder := filepath.Join(target.directory, name)
	if exists, err := agentdir.Exists(folder); err != nil {
		return "", 0, fmt.Errorf("inspect target folder: %w", err)
	} else if !exists {
		return SkillScopeMissing, 0, nil
	}

	disk, err := agentdir.Read(ctx, folder)
	if err != nil {
		if errors.Is(err, agentdir.ErrIrregularFile) {
			return SkillScopeModified, 0, nil
		}
		return "", 0, err
	}

	current, err := l.store.VersionDigests(ctx, currentVersionID)
	if err != nil {
		return "", 0, err
	}
	if maps.Equal(disk, current) {
		return SkillScopeCurrent, VersionID(currentVersionID), nil
	}

	versionIDs, err := l.store.OlderVersionIDs(ctx, int64(id), currentVersionID)
	if err != nil {
		return "", 0, err
	}
	for _, versionID := range versionIDs {
		files, err := l.store.VersionDigests(ctx, versionID)
		if err != nil {
			return "", 0, err
		}
		if maps.Equal(disk, files) {
			return SkillScopeStale, VersionID(versionID), nil
		}
	}
	return SkillScopeModified, 0, nil
}

// Reinstall 把列出的每一处换成技能在库里的当前版本,不改变留着它们的直接安装与组安装,SkillScope.CreatedAt 不变。
// 只换列出的几处:巡检之后才多出来的安装不会被一起换掉。逐处执行,结果与 requests 一一对应、顺序相同;
// 每一处各自要么换成功、要么保持原样,一处失败不影响其他处。本来就是当前版本的不动;落后的、文件夹不在了的
// (可重建 Agent 目录)换成当前版本;被改过的只在这一处的 Overwrite 同意时覆盖。
// 状态问题记在这一处的结果里(见 ReinstallResult),不让整次调用失败;只有请求本身不合法(Scope 组合非法、
// 同一处列了两次)才整次返回 InvalidArgument,且一处都不动。空列表返回空结果。
// 整体取消时返回已完成的几处与 Canceled 错误,未执行的不出现在结果里。
func (l *Library) Reinstall(ctx context.Context, requests []ReinstallRequest) (results []ReinstallResult, err error) {
	defer func() { err = typedError(err) }()
	const operation = "reinstall"
	type place struct {
		skillID SkillID
		scope   Scope
	}
	seen := make(map[place]bool, len(requests))
	for _, request := range requests {
		if _, err := scopeOf(operation, request.Scope); err != nil {
			return nil, err
		}
		key := place{skillID: request.SkillID, scope: request.Scope}
		if seen[key] {
			return nil, argumentError(operation, "requests", "the same skill scope is listed more than once")
		}
		seen[key] = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results = []ReinstallResult{}
	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := ReinstallResult{SkillID: request.SkillID, Scope: request.Scope}
		outcome, itemErr := l.reinstallOne(ctx, operation, request, 0)
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

func (l *Library) reinstallOne(ctx context.Context, operation string, request ReinstallRequest, expected int64) (ReinstallOutcome, error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	target, err := l.resolveScope(ctx, operation, request.Scope)
	if err != nil {
		return "", err
	}
	place, err := l.store.PlaceSkillScopes(ctx, int64(request.SkillID), target.record())
	if err != nil {
		return "", err
	}
	if len(place) == 0 {
		return "", &Error{Code: NotFound, Message: operation + ": skill is not installed in scope"}
	}
	skill, current, err := l.getCurrent(ctx, request.SkillID)
	if err != nil {
		return "", err
	}
	if expected != 0 && current.versionID != expected {
		return "", &Error{Code: Conflict, Message: operation + ": skill version changed since the update", Details: ErrorDetails{SkillID: request.SkillID}}
	}
	if err := target.requireRoot(operation); err != nil {
		return "", err
	}
	status, _, err := l.inspectSkillScope(ctx, skill.ID, target)
	if err != nil {
		return "", err
	}
	switch status {
	case SkillScopeCurrent:
		return ReinstallCurrent, nil
	case SkillScopeModified:
		if !request.Overwrite {
			return "", existsError(operation, "installed folder was modified outside the library", ErrorDetails{Path: filepath.Join(target.directory, skill.Name)})
		}
	}
	if err := l.place(ctx, []*placement{{skill: skill, files: current.files, scope: target}}, nil); err != nil {
		return "", err
	}
	return Reinstalled, nil
}
