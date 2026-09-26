package library

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
)

func (l *sqliteLibrary) RewriteProjections(ctx context.Context, filter ProjectionFilter, opts RewriteOptions) (results []RewriteResult, err error) {
	// 逐条各持独占锁,条与条之间释放,批量重写期间列表读取不被整批挡住。
	defer func() { err = typedError(err) }()
	const operation = "rewrite projections"
	if err := validateConflictPolicy(operation, opts.OnConflict); err != nil {
		return nil, err
	}
	if filter.SkillID == 0 && filter.Scope == nil {
		return nil, argumentError(operation, "filter", "rewrite requires a skill or a scope")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	projections, err := l.filteredProjections(ctx, operation, filter)
	if err != nil {
		return nil, err
	}
	results = []RewriteResult{}
	for _, projection := range projections {
		// 照 CheckUpdates:取消时返回已完成的各条,未执行的不出现在结果里。
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := RewriteResult{SkillID: projection.SkillID, Scope: projection.Scope}
		outcome, itemErr := l.rewriteProjection(ctx, operation, projection, opts.OnConflict)
		if itemErr != nil && ctx.Err() != nil && errors.Is(itemErr, ctx.Err()) {
			// 这一条因取消已复原,不算完成;取消前已判定的失败(如 modified)照常计入结果。
			return results, ctx.Err()
		}
		if itemErr != nil {
			result.Outcome, result.Error = RewriteFailed, ErrorOf(itemErr)
		} else {
			result.Outcome = outcome
		}
		results = append(results, result)
	}
	return results, nil
}

// rewriteProjection 在独占锁内把一条投影带到技能的当前版本,不碰任何激活记录;返回的错误即该条失败的原因,
// 此时磁盘保持原样。列出与执行之间锁已释放,因此重新解析目录并确认记录仍在。
func (l *sqliteLibrary) rewriteProjection(ctx context.Context, operation string, projection Projection, policy ConflictPolicy) (RewriteOutcome, error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	target, err := l.resolveScope(ctx, operation, projection.Scope)
	if err != nil {
		return "", err
	}
	existing, err := findProjection(ctx, l.db, projection.SkillID, target.scopeRow)
	if err != nil {
		return "", err
	}
	if existing == nil {
		return "", &Error{Code: NotFound, Message: operation + ": projection no longer exists"}
	}
	skill, _, err := l.getSkill(ctx, projection.SkillID)
	if err != nil {
		return "", err
	}
	// 根目录不存在时不重建 directory_missing 的投影,也不创建任何目录。
	if err := target.requireRoot(operation); err != nil {
		return "", err
	}
	status, err := l.inspectProjection(ctx, Projection{SkillID: skill.ID, Directory: target.directory})
	if err != nil {
		return "", err
	}
	switch status {
	case ProjectionCurrent:
		return RewriteCurrent, nil
	case ProjectionModified:
		if policy != Overwrite {
			return "", preconditionError(operation, "projection was modified outside the library", ErrorDetails{Path: filepath.Join(target.directory, skill.Name)})
		}
	}
	// 与激活共用写盘路径(暂存 + 原子替换 + 失败复原),只是不写任何记录。
	if err := l.place(ctx, []*placement{{skill: skill, scope: target}}, func(*sql.Tx) error { return nil }); err != nil {
		return "", err
	}
	return Rewritten, nil
}
