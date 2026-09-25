package library

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"
)

// scopeRow 是 Scope 身份的落库形状:Agent 代码 + 可选项目(nil 为全局)。
type scopeRow struct {
	agent     AgentCode
	projectID *ProjectID
}

// scopeOf 校验 Scope 的组合并拆成落库列;非法组合按出错字段报 InvalidArgument。
func scopeOf(operation string, scope Scope) (scopeRow, error) {
	switch scope.Kind {
	case ScopeGlobal:
		if scope.ProjectID != 0 {
			return scopeRow{}, argumentError(operation, "project_id", "global scope must not have a project")
		}
		return scopeRow{agent: scope.Agent}, nil
	case ScopeProject:
		if scope.ProjectID <= 0 {
			return scopeRow{}, argumentError(operation, "project_id", "project scope requires a project")
		}
		return scopeRow{agent: scope.Agent, projectID: new(scope.ProjectID)}, nil
	default:
		return scopeRow{}, argumentError(operation, "kind", "unknown scope kind")
	}
}

// scope 把落库列还原为门面上的 Scope 身份。
func (r scopeRow) scope() Scope {
	if r.projectID == nil {
		return GlobalScope(r.agent)
	}
	return ProjectScope(r.agent, *r.projectID)
}

// resolvedScope 是一次激活操作的内部目标:Scope 的落库身份 + 按现行布局算出的目录。
type resolvedScope struct {
	scopeRow
	directory string
}

// resolveScope 把门面收到的 Scope 变成内部目标:先按调用方的操作名校验组合,
// 未知 Agent / 未登记项目的 NotFound 来自 Resolve。
func (l *sqliteLibrary) resolveScope(ctx context.Context, operation string, scope Scope) (resolvedScope, error) {
	row, err := scopeOf(operation, scope)
	if err != nil {
		return resolvedScope{}, err
	}
	directory, err := l.Resolve(ctx, scope)
	if err != nil {
		return resolvedScope{}, err
	}
	return resolvedScope{scopeRow: row, directory: directory}, nil
}

// rowQuerier 让 *sql.DB 与 *sql.Tx 共用同一个单行查询。
type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// projectionRow 是一条已存在的投影记录:稳定 ID 与直接激活标记。
type projectionRow struct {
	id         int64
	individual bool
}

// findProjection 找到某 skill 在某 Scope 的投影记录;不存在时返回 nil 而非错误。
func findProjection(ctx context.Context, q rowQuerier, id SkillID, scope scopeRow) (*projectionRow, error) {
	var row projectionRow
	err := q.QueryRowContext(ctx, `SELECT id, individual FROM activations WHERE skill_id = ? AND agent = ? AND project_id IS ?`, id, scope.agent, scope.projectID).Scan(&row.id, &row.individual)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find activation: %w", err)
	}
	return &row, nil
}

// projectionTrash 让"删记录 + 删投影文件夹"可逆:调用方在事务内删完记录、把待删路径放进 targets,
// 再由 commit 收尾:改名到同一父目录下的暂存名 → 提交事务 → 真正删除;提交前任一步失败改回原名,
// 配合调用方的 Rollback 使库和磁盘都回到操作前。
// RemoveSkill / UnregisterProject / Deactivate / DeactivateGroup 共用,避免任何一处再出现"先删盘后写库"。
type projectionTrash struct {
	targets []string
	trashed []struct{ trash, target string } // 已改名的条目:原路径与暂存路径
}

// commit 完成收尾;operation 用于提交失败的错误消息。暂存目录的最终删除失败只留下垃圾,不影响权威数据。
func (t *projectionTrash) commit(ctx context.Context, tx *sql.Tx, operation string) error {
	if err := t.trash(ctx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(fmt.Errorf("commit %s: %w", operation, err), t.restore())
	}
	for _, entry := range t.trashed {
		_ = os.RemoveAll(entry.trash)
	}
	return nil
}

// trash 把每个存在的目标改名到暂存;不存在的跳过。任一步失败自动 restore 并返回错误。
func (t *projectionTrash) trash(ctx context.Context) error {
	for _, path := range t.targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, t.restore())
		}
		staged, err := randomName(filepath.Join(filepath.Dir(path), ".remove-"))
		if err != nil {
			return errors.Join(err, t.restore())
		}
		if err := os.Rename(path, staged); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return errors.Join(fmt.Errorf("remove projected copy: %w", err), t.restore())
		}
		t.trashed = append(t.trashed, struct{ trash, target string }{staged, path})
	}
	return nil
}

// restore 把全部已改名的暂存目录改回原名,供提交前失败时复位。
func (t *projectionTrash) restore() error {
	var restoreErr error
	for _, entry := range t.trashed {
		restoreErr = errors.Join(restoreErr, os.Rename(entry.trash, entry.target))
	}
	return restoreErr
}

// claimant 是一条可能与待激活目标争同一文件夹的既有投影:所属 skill、投影名与 Scope 身份。
type claimant struct {
	skillID SkillID
	name    string
	scopeRow
}

// targetClaimant 在写事务外查找目标占用者,供预检与执行共用。调用方持共享或独占锁,
// 并已确认本技能在本作用域没有记录;所有目录均按现行布局解析。
func (l *sqliteLibrary) targetClaimant(ctx context.Context, id SkillID, name string, target resolvedScope) (*claimant, error) {
	// 同名即包含本 skill 自身;本 skill 的其他 Scope 排在前面,使既有的"共享目录"错误形状不因同名 skill 的存在而改变。
	rows, err := l.db.QueryContext(ctx, `
		SELECT a.skill_id, s.name, a.agent, a.project_id FROM activations a
		JOIN skills s ON s.id = a.skill_id
		WHERE s.name = ? ORDER BY a.skill_id <> ?`, name, id)
	if err != nil {
		return nil, fmt.Errorf("list claimants: %w", err)
	}
	var claimants []claimant
	for rows.Next() {
		var row claimant
		if err := rows.Scan(&row.skillID, &row.name, &row.agent, &row.projectID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read claimant: %w", err)
		}
		claimants = append(claimants, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read claimants: %w", err)
	}
	for _, other := range claimants {
		directory, err := l.Resolve(ctx, other.scope())
		if err != nil {
			return nil, err
		}
		if directory != target.directory {
			continue
		}
		return &other, nil
	}
	return nil, nil
}

// requireTargetUnclaimed 把占用事实映射为执行错误,保留同技能跨作用域的既有细节形状。
func (l *sqliteLibrary) requireTargetUnclaimed(ctx context.Context, operation string, id SkillID, name string, target resolvedScope) error {
	other, err := l.targetClaimant(ctx, id, name, target)
	if err != nil {
		return err
	}
	if other == nil {
		return nil
	}
	if other.skillID == id {
		return preconditionError(operation, "another scope already projects this skill into the same directory", ErrorDetails{Path: target.directory})
	}
	return preconditionError(operation, "another skill already projects this name into the same directory", ErrorDetails{
		Path: filepath.Join(target.directory, other.name), Name: other.name, SkillID: other.skillID,
	})
}

func (l *sqliteLibrary) PreviewActivate(ctx context.Context, id SkillID, scope Scope) (preview ActivatePreview, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "preview activate", scope)
	if err != nil {
		return preview, err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return preview, err
	}
	return l.previewActivate(ctx, skill.ID, skill.Name, target)
}

func (l *sqliteLibrary) PreviewActivateGroup(ctx context.Context, groupID GroupID, scope Scope) (preview GroupActivatePreview, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "preview activate group", scope)
	if err != nil {
		return preview, err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return preview, err
	}
	counts := make(map[string]int)
	for _, member := range members {
		counts[member.Name]++
		if counts[member.Name] == 2 {
			preview.DuplicateNames = append(preview.DuplicateNames, member.Name)
		}
	}
	preview.Members = make([]ActivatePreview, 0, len(members))
	for _, member := range members {
		item, err := l.previewActivate(ctx, member.ID, member.Name, target)
		if err != nil {
			return GroupActivatePreview{}, err
		}
		preview.Members = append(preview.Members, item)
	}
	return preview, nil
}

func (l *sqliteLibrary) previewActivate(ctx context.Context, id SkillID, name string, scope resolvedScope) (ActivatePreview, error) {
	preview := ActivatePreview{SkillID: id, Name: name, Directory: scope.directory, Target: filepath.Join(scope.directory, name), Status: ActivateReady}
	existing, err := findProjection(ctx, l.db, id, scope.scopeRow)
	if err != nil {
		return preview, err
	}
	if existing != nil {
		preview.Status = ActivateProjected
		return preview, nil
	}
	other, err := l.targetClaimant(ctx, id, name, scope)
	if err != nil {
		return preview, err
	}
	if other != nil {
		preview.Status = ActivateOccupied
		preview.OccupierSkillID = other.skillID
		preview.OccupierName = other.name
		return preview, nil
	}
	if _, err := os.Lstat(preview.Target); err == nil {
		preview.Status = ActivateUntracked
	} else if !errors.Is(err, os.ErrNotExist) {
		return preview, fmt.Errorf("inspect activation target: %w", err)
	}
	return preview, nil
}

func (l *sqliteLibrary) Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("activate", opts.OnConflict); err != nil {
		return err
	}
	target, err := l.resolveScope(ctx, "activate", scope)
	if err != nil {
		return err
	}
	return l.activate(ctx, id, target, opts, true, 0)
}

func (l *sqliteLibrary) activate(ctx context.Context, id SkillID, scope resolvedScope, opts ActivateOptions, individual bool, groupID GroupID) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	// 已有本 skill 在该 Scope 的投影记录时,目标目录是我们自己的投影,再次激活应当
	// 幂等成功(覆盖为当前版本 / 补记激活),而非因目录存在而报冲突。
	existing, err := findProjection(ctx, l.db, id, scope.scopeRow)
	if err != nil {
		return err
	}
	if existing == nil {
		if err := l.requireTargetUnclaimed(ctx, "activate skill", skill.ID, skill.Name, scope); err != nil {
			return err
		}
	}
	target := filepath.Join(scope.directory, skill.Name)
	if _, err := os.Lstat(target); err == nil {
		if existing == nil && opts.OnConflict != Overwrite {
			return existsError("activate skill", "target already exists", ErrorDetails{Path: target})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect activation target: %w", err)
	}
	if err := os.MkdirAll(scope.directory, 0o755); err != nil {
		return fmt.Errorf("create scope: %w", err)
	}
	stage, err := stageSkill(scope.directory, skill)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	backup, err := moveStageIntoPlace(stage, target)
	if err != nil {
		return err
	}
	restore := func() {
		_ = os.RemoveAll(target)
		if backup != "" {
			_ = os.Rename(backup, target)
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		restore()
		return fmt.Errorf("begin activation record: %w", err)
	}
	defer tx.Rollback()
	if err := recordActivationTx(ctx, tx, id, scope.scopeRow, individual, groupID, clockNow()); err != nil {
		restore()
		return err
	}
	if err := tx.Commit(); err != nil {
		restore()
		return fmt.Errorf("commit activation record: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// stageSkill 把 skill 的全部文件写入 directory 下的一个临时暂存目录,返回暂存路径。
// 暂存好但尚未移入目标位置,便于组激活先把所有成员备齐再统一落盘。
func stageSkill(directory string, skill Skill) (string, error) {
	stage, err := os.MkdirTemp(directory, "."+skill.Name+"-")
	if err != nil {
		return "", fmt.Errorf("stage activation: %w", err)
	}
	for _, file := range skill.Files {
		if !filepath.IsLocal(file.Path) {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("invalid skill path: %s", file.Path)
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("create activation directory: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("write activation file: %w", err)
		}
	}
	return stage, nil
}

// moveStageIntoPlace 把暂存目录移到目标位置;目标已存在则先备份到返回的 backup 路径
// (backup 为空表示目标原本不存在)。失败时尽力把备份复位。
func moveStageIntoPlace(stage, target string) (backup string, err error) {
	if _, err := os.Lstat(target); err == nil {
		backup = stage + ".old"
		if err := os.Rename(target, backup); err != nil {
			return "", fmt.Errorf("replace activation: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect activation target: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return "", fmt.Errorf("activate skill: %w", err)
	}
	return backup, nil
}

// recordActivationTx 在事务内记一个激活:没有投影记录时先建一条,再补直接激活标记或组激活行。
// 已有记录只补直接激活标记;新记录只带 Scope 身份,不存目录。
func recordActivationTx(ctx context.Context, tx *sql.Tx, id SkillID, scope scopeRow, individual bool, groupID GroupID, activatedAt time.Time) error {
	var projectionID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO activations(skill_id, agent, project_id, individual, activated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET individual = MAX(individual, excluded.individual)
		RETURNING id`, id, scope.agent, scope.projectID, individual, formatStoredTime(activatedAt)).Scan(&projectionID)
	if err != nil {
		return fmt.Errorf("record activation: %w", err)
	}
	if groupID != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO activation_groups(activation_id, group_id) VALUES (?, ?)`, projectionID, groupID); err != nil {
			return fmt.Errorf("record group activation: %w", err)
		}
	}
	return nil
}

func (l *sqliteLibrary) Deactivate(ctx context.Context, id SkillID, scope Scope) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "deactivate", scope)
	if err != nil {
		return err
	}
	existing, err := findProjection(ctx, l.db, id, target.scopeRow)
	if err != nil {
		return err
	}
	if existing == nil {
		return &Error{Code: NotFound, Message: "deactivate skill: skill is not active in scope"}
	}
	if !existing.individual {
		name, err := supportingGroupName(ctx, l.db, existing.id)
		if err != nil {
			return err
		}
		return preconditionError("deactivate skill", "activation is only via group", ErrorDetails{Name: name})
	}
	return l.deactivateDirect(ctx, id, existing.id, target.directory)
}

// deactivateDirect 撤销直接激活;没有其他激活撑着时连同磁盘上的投影一起删除该记录。
func (l *sqliteLibrary) deactivateDirect(ctx context.Context, id SkillID, projectionID int64, directory string) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deactivation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE activations SET individual = 0 WHERE id = ?`, projectionID); err != nil {
		return fmt.Errorf("remove independent activation: %w", err)
	}
	keep, err := keepProjection(ctx, tx, projectionID)
	if err != nil {
		return err
	}
	var trash projectionTrash
	if !keep {
		if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE id = ?`, projectionID); err != nil {
			return fmt.Errorf("remove activation: %w", err)
		}
		trash.targets = []string{filepath.Join(directory, skill.Name)}
	}
	return trash.commit(ctx, tx, "deactivation")
}

// keepProjection 在撤销一个激活后判断是否仍有激活撑着该投影。
func keepProjection(ctx context.Context, tx *sql.Tx, projectionID int64) (bool, error) {
	var keep bool
	if err := tx.QueryRowContext(ctx, `
		SELECT individual OR EXISTS(SELECT 1 FROM activation_groups WHERE activation_id = ?)
		FROM activations WHERE id = ?
	`, projectionID, projectionID).Scan(&keep); err != nil {
		return false, fmt.Errorf("check activation origins: %w", err)
	}
	return keep, nil
}

func (l *sqliteLibrary) ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) (err error) {
	// 整组操作持独占锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("activate group", opts.OnConflict); err != nil {
		return err
	}
	target, err := l.resolveScope(ctx, "activate group", scope)
	if err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return err
	}
	// 组成员可跨来源并重名,但同名成员会投影到同一路径;整批落盘前拒绝这种冲突。
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		if seen[member.Name] {
			return preconditionError("activate group", "multiple members project to the same target", ErrorDetails{Name: member.Name})
		}
		seen[member.Name] = true
	}
	for _, member := range members {
		// 已属本组/本 skill 自己的既有投影不算冲突,组再次激活应幂等成功。
		existing, err := findProjection(ctx, l.db, member.ID, target.scopeRow)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		if err := l.requireTargetUnclaimed(ctx, "activate group", member.ID, member.Name, target); err != nil {
			return err
		}
		if opts.OnConflict == Overwrite {
			continue
		}
		targetPath := filepath.Join(target.directory, member.Name)
		if _, err := os.Lstat(targetPath); err == nil {
			return existsError("activate group", "target already exists", ErrorDetails{Path: targetPath})
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect activation target: %w", err)
		}
	}
	if len(members) == 0 {
		return nil
	}
	if err := os.MkdirAll(target.directory, 0o755); err != nil {
		return fmt.Errorf("create scope: %w", err)
	}

	// 全组作为一个操作单元:先把每个成员备齐并统一落盘,再在单事务里记录;
	// 任何一步失败都回滚本次操作的磁盘改动与记录,既有的直接激活不受影响。
	type staged struct {
		id     SkillID
		target string
		stage  string // 暂存目录;移入目标后置空
		backup string // 被替换的原目标备份;"" 表示原本无目标
		moved  bool   // 暂存是否已移入目标位置
	}
	var applied []*staged
	rollback := func() error {
		var restoreErr error
		for i := len(applied) - 1; i >= 0; i-- {
			member := applied[i]
			if member.moved {
				if err := os.RemoveAll(member.target); err != nil {
					restoreErr = errors.Join(restoreErr, err)
				} else if member.backup != "" {
					// 复位失败时保留备份,避免丢失用户原始内容。
					restoreErr = errors.Join(restoreErr, os.Rename(member.backup, member.target))
				}
			}
			if member.stage != "" {
				restoreErr = errors.Join(restoreErr, os.RemoveAll(member.stage))
			}
		}
		return restoreErr
	}

	for _, member := range members {
		skill, err := l.GetSkill(ctx, member.ID)
		if err != nil {
			return errors.Join(err, rollback())
		}
		stage, err := stageSkill(target.directory, skill)
		if err != nil {
			return errors.Join(err, rollback())
		}
		applied = append(applied, &staged{id: skill.ID, target: filepath.Join(target.directory, skill.Name), stage: stage})
	}
	for _, member := range applied {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollback())
		}
		backup, err := moveStageIntoPlace(member.stage, member.target)
		if err != nil {
			return errors.Join(err, rollback())
		}
		member.backup = backup
		member.moved = true
		member.stage = ""
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(fmt.Errorf("begin group activation: %w", err), rollback())
	}
	defer tx.Rollback()
	for _, member := range applied {
		if err := recordActivationTx(ctx, tx, member.id, target.scopeRow, false, groupID, clockNow()); err != nil {
			return errors.Join(err, rollback())
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(fmt.Errorf("commit group activation: %w", err), rollback())
	}
	for _, member := range applied {
		if member.backup != "" {
			_ = os.RemoveAll(member.backup)
		}
	}
	return nil
}

func (l *sqliteLibrary) DeactivateGroup(ctx context.Context, groupID GroupID, scope Scope) (err error) {
	// 整组操作持独占锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "deactivate group", scope)
	if err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT a.id, a.skill_id FROM activation_groups g JOIN activations a ON a.id = g.activation_id
		WHERE g.group_id = ? AND a.agent = ? AND a.project_id IS ? ORDER BY a.skill_id`, groupID, target.agent, target.projectID)
	if err != nil {
		return fmt.Errorf("list group activations: %w", err)
	}
	type groupProjection struct {
		projectionID int64
		skillID      SkillID
	}
	var members []groupProjection
	for rows.Next() {
		var member groupProjection
		if err := rows.Scan(&member.projectionID, &member.skillID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read group activation: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close group activations: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read group activations: %w", err)
	}
	if len(members) == 0 {
		return &Error{Code: NotFound, Message: "deactivate group: group is not active in scope"}
	}

	// 单连接约束下,先在开事务前把成员名字取齐(事务期间不能再走 l.db)。
	names := make(map[SkillID]string, len(members))
	for _, member := range members {
		skill, err := l.GetSkill(ctx, member.skillID)
		if err != nil {
			return err
		}
		names[member.skillID] = skill.Name
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group deactivation: %w", err)
	}
	defer tx.Rollback()
	var trash projectionTrash
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE activation_id = ? AND group_id = ?`, member.projectionID, groupID); err != nil {
			return fmt.Errorf("remove group activation: %w", err)
		}
		keep, err := keepProjection(ctx, tx, member.projectionID)
		if err != nil {
			return err
		}
		if !keep {
			if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE id = ?`, member.projectionID); err != nil {
				return fmt.Errorf("remove activation: %w", err)
			}
			trash.targets = append(trash.targets, filepath.Join(target.directory, names[member.skillID]))
		}
	}

	return trash.commit(ctx, tx, "group deactivation")
}

// supportingGroupName 读取仅由组激活撑着的投影所属的第一个 Group 名。
func supportingGroupName(ctx context.Context, q rowQuerier, projectionID int64) (string, error) {
	var name string
	err := q.QueryRowContext(ctx, `
		SELECT groups.name FROM activation_groups
		JOIN groups ON groups.id = activation_groups.group_id
		WHERE activation_groups.activation_id = ?
		ORDER BY groups.id LIMIT 1`, projectionID).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("read supporting group: %w", err)
	}
	return name, nil
}

func randomName(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}

func (l *sqliteLibrary) ListProjections(ctx context.Context) (projections []Projection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	// 全局(project_id 为 NULL)排在同一 Agent 的各项目 Scope 之前。
	rows, err := l.db.QueryContext(ctx, `SELECT id, skill_id, agent, project_id, individual, activated_at FROM activations ORDER BY skill_id, agent, project_id`)
	if err != nil {
		return nil, fmt.Errorf("list activations: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var (
			projection  Projection
			id          int64
			row         scopeRow
			activatedAt string
		)
		if err := rows.Scan(&id, &projection.SkillID, &row.agent, &row.projectID, &projection.Individual, &activatedAt); err != nil {
			return nil, fmt.Errorf("read activation: %w", err)
		}
		if projection.ActivatedAt, err = parseStoredTime(activatedAt); err != nil {
			return nil, fmt.Errorf("read activation: %w", err)
		}
		projection.Scope = row.scope()
		ids = append(ids, id)
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read activations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close activations: %w", err)
	}
	for i := range projections {
		// 目录是派生值:按现行布局 Resolve,库里不存路径。
		if projections[i].Directory, err = l.Resolve(ctx, projections[i].Scope); err != nil {
			return nil, err
		}
		rows, err := l.db.QueryContext(ctx, `SELECT group_id FROM activation_groups WHERE activation_id = ? ORDER BY group_id`, ids[i])
		if err != nil {
			return nil, fmt.Errorf("list activation groups: %w", err)
		}
		for rows.Next() {
			var id GroupID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("read activation group: %w", err)
			}
			projections[i].ViaGroups = append(projections[i].ViaGroups, id)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close activation groups: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read activation groups: %w", err)
		}
	}
	return projections, nil
}

func (l *sqliteLibrary) InspectProjections(ctx context.Context) (inspections []ProjectionInspection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	projections, err := l.ListProjections(ctx)
	if err != nil {
		return nil, err
	}
	for _, projection := range projections {
		status, err := l.inspectProjection(ctx, projection)
		if err != nil {
			return nil, err
		}
		inspections = append(inspections, ProjectionInspection{Projection: projection, Status: status})
	}
	return inspections, nil
}

// inspectProjection 按 ProjectionStatus 注释中的顺序判定一条投影。
func (l *sqliteLibrary) inspectProjection(ctx context.Context, projection Projection) (ProjectionStatus, error) {
	var name string
	var currentVersionID int64
	if err := l.db.QueryRowContext(ctx, `SELECT name, current_version_id FROM skills WHERE id = ?`, projection.SkillID).Scan(&name, &currentVersionID); err != nil {
		return "", fmt.Errorf("inspect activation: %w", err)
	}

	if _, err := os.Lstat(projection.Directory); errors.Is(err, os.ErrNotExist) {
		return ProjectionDirectoryMissing, nil
	} else if err != nil {
		return "", fmt.Errorf("inspect activation directory: %w", err)
	}

	target := filepath.Join(projection.Directory, name)
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return ProjectionMissing, nil
	} else if err != nil {
		return "", fmt.Errorf("inspect activation target: %w", err)
	}

	disk, err := hashFiles(ctx, target)
	if err != nil {
		if errors.Is(err, errIrregularFile) {
			return ProjectionModified, nil
		}
		return "", err
	}

	current, err := l.versionFingerprints(ctx, currentVersionID)
	if err != nil {
		return "", err
	}
	if maps.Equal(disk, current) {
		return ProjectionCurrent, nil
	}

	rows, err := l.db.QueryContext(ctx, `SELECT id FROM skill_versions WHERE skill_id = ? AND id != ? ORDER BY id DESC`, projection.SkillID, currentVersionID)
	if err != nil {
		return "", fmt.Errorf("list skill versions: %w", err)
	}
	var versionIDs []int64
	for rows.Next() {
		var versionID int64
		if err := rows.Scan(&versionID); err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("read skill version: %w", err)
		}
		versionIDs = append(versionIDs, versionID)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", fmt.Errorf("read skill versions: %w", err)
	}
	for _, versionID := range versionIDs {
		files, err := l.versionFingerprints(ctx, versionID)
		if err != nil {
			return "", err
		}
		if maps.Equal(disk, files) {
			return ProjectionStale, nil
		}
	}
	return ProjectionModified, nil
}

// versionFingerprints 只读 version_files 的 path/fingerprint/mode,不取 blob 正文。
func (l *sqliteLibrary) versionFingerprints(ctx context.Context, versionID int64) (map[string]fileMeta, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT path, fingerprint, mode FROM version_files WHERE version_id = ?`, versionID)
	if err != nil {
		return nil, fmt.Errorf("list version fingerprints: %w", err)
	}
	defer rows.Close()
	files := make(map[string]fileMeta)
	for rows.Next() {
		var path string
		var meta fileMeta
		if err := rows.Scan(&path, &meta.fingerprint, &meta.mode); err != nil {
			return nil, fmt.Errorf("read version fingerprint: %w", err)
		}
		files[path] = meta
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read version fingerprints: %w", err)
	}
	return files, nil
}
