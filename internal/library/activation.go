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

// groupCoverageSQL 判断 projections 当前行是否被组激活覆盖:该 skill 是某组成员且该组已激活到同一 Scope。
// 带一个参数:要排除的 GroupID(0 表示不排除)。组激活撑着投影的事实只由它推出,不另存。
const groupCoverageSQL = `EXISTS (
	SELECT 1 FROM group_activations ga JOIN skill_groups sg ON sg.group_id = ga.group_id
	WHERE sg.skill_id = projections.skill_id AND ga.agent = projections.agent
		AND ga.project_id IS projections.project_id AND ga.group_id <> ?)`

// projectionRow 是一条已存在的投影记录:稳定 ID 与直接激活标记。
type projectionRow struct {
	id     int64
	direct bool
}

// findProjection 找到某 skill 在某 Scope 的投影记录;不存在时返回 nil 而非错误。
func findProjection(ctx context.Context, q rowQuerier, id SkillID, scope scopeRow) (*projectionRow, error) {
	var row projectionRow
	err := q.QueryRowContext(ctx, `SELECT id, direct FROM projections WHERE skill_id = ? AND agent = ? AND project_id IS ?`, id, scope.agent, scope.projectID).Scan(&row.id, &row.direct)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find projection: %w", err)
	}
	return &row, nil
}

// dropUnbackedProjection 在撤销某个激活后,删掉不再有任何激活撑着的投影记录;返回是否删除。
func dropUnbackedProjection(ctx context.Context, tx *sql.Tx, projectionID int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM projections WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, projectionID, 0)
	if err != nil {
		return false, fmt.Errorf("remove projection: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove projection: %w", err)
	}
	return removed > 0, nil
}

// projectionTrash 让"删记录 + 删投影文件夹"可逆:调用方在事务内删完记录、把待删路径放进 targets,
// 再由 commit 收尾:改名到同一父目录下的暂存名 → 提交事务 → 真正删除;提交前任一步失败改回原名,
// 配合调用方的 Rollback 使库和磁盘都回到操作前。
// RemoveSkill / UnregisterProject / Deactivate / DeactivateGroup / RemoveSkillFromGroup 共用,
// 避免任何一处再出现"先删盘后写库"。
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
		SELECT p.skill_id, s.name, p.agent, p.project_id FROM projections p
		JOIN skills s ON s.id = p.skill_id
		WHERE s.name = ? ORDER BY p.skill_id <> ?`, name, id)
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

// placementConflict 检查尚无投影的 skill 能否在 target 新建投影:目标被另一条投影占用返回 FailedPrecondition,
// 有未记录的文件夹且未选 Overwrite 返回 AlreadyExists;能投影返回 nil。查询或磁盘读取失败放在第二个返回值。
func (l *sqliteLibrary) placementConflict(ctx context.Context, operation string, id SkillID, name string, target resolvedScope, policy ConflictPolicy) (*Error, error) {
	other, err := l.targetClaimant(ctx, id, name, target)
	if err != nil {
		return nil, err
	}
	if other != nil && other.skillID == id {
		return preconditionError(operation, "another scope already projects this skill into the same directory", ErrorDetails{Path: target.directory}), nil
	}
	if other != nil {
		return preconditionError(operation, "another skill already projects this name into the same directory", ErrorDetails{
			Path: filepath.Join(target.directory, other.name), Name: other.name, SkillID: other.skillID,
		}), nil
	}
	path := filepath.Join(target.directory, name)
	if _, err := os.Lstat(path); err == nil {
		if policy != Overwrite {
			return existsError(operation, "target already exists", ErrorDetails{Path: path}), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect projection target: %w", err)
	}
	return nil, nil
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
		return preview, fmt.Errorf("inspect projection target: %w", err)
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
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	existing, err := findProjection(ctx, l.db, id, target.scopeRow)
	if err != nil {
		return err
	}
	if existing != nil {
		// 已有投影:只补上直接激活,不写盘;把文件夹带到当前版本是单独的重写。
		if _, err := l.db.ExecContext(ctx, `UPDATE projections SET direct = 1 WHERE id = ?`, existing.id); err != nil {
			return fmt.Errorf("record direct activation: %w", err)
		}
		return nil
	}
	conflict, err := l.placementConflict(ctx, "activate skill", skill.ID, skill.Name, target, opts.OnConflict)
	if err != nil {
		return err
	}
	if conflict != nil {
		return conflict
	}
	placements := []*placement{{skill: skill, scope: target}}
	return l.place(ctx, placements, func(tx *sql.Tx) error {
		return recordProjectionTx(ctx, tx, skill.ID, target.scopeRow, true, clockNow())
	})
}

// placement 是一次落盘里要新建的一个投影:把 skill 的当前版本写进 scope 的目录。
type placement struct {
	skill  Skill
	scope  resolvedScope
	target string // 目标文件夹
	stage  string // 暂存目录;移入目标后置空
	backup string // 被替换的未记录目标(Overwrite)的备份;"" 表示原本无目标
	moved  bool   // 暂存是否已移入目标位置
}

// place 把一批新投影作为一个操作单元落盘并记录:先全部暂存,再逐个移入目标,最后在单事务里调用 record。
// 任何一步失败都复原本次的磁盘改动(含被替换的未记录文件夹)且不留记录,既有投影不受影响。
// placements 为空时只执行 record。调用方持独占锁并已排除冲突。
func (l *sqliteLibrary) place(ctx context.Context, placements []*placement, record func(*sql.Tx) error) error {
	rollback := func() error {
		var restoreErr error
		for i := len(placements) - 1; i >= 0; i-- {
			item := placements[i]
			if item.moved {
				if err := os.RemoveAll(item.target); err != nil {
					restoreErr = errors.Join(restoreErr, err)
				} else if item.backup != "" {
					// 复位失败时保留备份,避免丢失用户原始内容。
					restoreErr = errors.Join(restoreErr, os.Rename(item.backup, item.target))
				}
			}
			if item.stage != "" {
				restoreErr = errors.Join(restoreErr, os.RemoveAll(item.stage))
			}
		}
		return restoreErr
	}
	for _, item := range placements {
		if err := os.MkdirAll(item.scope.directory, 0o755); err != nil {
			return errors.Join(fmt.Errorf("create scope: %w", err), rollback())
		}
		stage, err := stageSkill(item.scope.directory, item.skill)
		if err != nil {
			return errors.Join(err, rollback())
		}
		item.stage = stage
		item.target = filepath.Join(item.scope.directory, item.skill.Name)
	}
	for _, item := range placements {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollback())
		}
		backup, err := moveStageIntoPlace(item.stage, item.target)
		if err != nil {
			return errors.Join(err, rollback())
		}
		item.backup = backup
		item.moved = true
		item.stage = ""
	}
	// 最后一个移入后、写记录前再看一次取消,已落盘的这批仍可整体复原。
	if err := ctx.Err(); err != nil {
		return errors.Join(err, rollback())
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(fmt.Errorf("begin projection record: %w", err), rollback())
	}
	defer tx.Rollback()
	if err := record(tx); err != nil {
		return errors.Join(err, rollback())
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(fmt.Errorf("commit projection record: %w", err), rollback())
	}
	for _, item := range placements {
		if item.backup != "" {
			_ = os.RemoveAll(item.backup)
		}
	}
	return nil
}

// stageSkill 把 skill 的全部文件写入 directory 下的一个临时暂存目录,返回暂存路径。
// 暂存好但尚未移入目标位置,便于一批投影先全部备齐再统一落盘。
func stageSkill(directory string, skill Skill) (string, error) {
	stage, err := os.MkdirTemp(directory, "."+skill.Name+"-")
	if err != nil {
		return "", fmt.Errorf("stage projection: %w", err)
	}
	for _, file := range skill.Files {
		if !filepath.IsLocal(file.Path) {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("invalid skill path: %s", file.Path)
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("create projection directory: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("write projection file: %w", err)
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
			return "", fmt.Errorf("replace projection: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect projection target: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return "", fmt.Errorf("place projection: %w", err)
	}
	return backup, nil
}

// recordProjectionTx 在事务内为刚落盘的投影建记录;direct 表示是否由直接激活撑着。只带 Scope 身份,不存目录。
func recordProjectionTx(ctx context.Context, tx *sql.Tx, id SkillID, scope scopeRow, direct bool, activatedAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO projections(skill_id, agent, project_id, direct, activated_at) VALUES (?, ?, ?, ?, ?)`,
		id, scope.agent, scope.projectID, direct, formatStoredTime(activatedAt)); err != nil {
		return fmt.Errorf("record projection: %w", err)
	}
	return nil
}

// recordGroupActivationTx 在事务内记下组在 Scope 的组激活;已有则保持原记录(含时间)。
func recordGroupActivationTx(ctx context.Context, tx *sql.Tx, groupID GroupID, scope scopeRow, activatedAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO group_activations(group_id, agent, project_id, activated_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		groupID, scope.agent, scope.projectID, formatStoredTime(activatedAt)); err != nil {
		return fmt.Errorf("record group activation: %w", err)
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
	if !existing.direct {
		name, err := coveringGroupName(ctx, l.db, id, target.scopeRow)
		if err != nil {
			return err
		}
		return preconditionError("deactivate skill", "activation is only via group", ErrorDetails{Name: name})
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deactivation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE projections SET direct = 0 WHERE id = ?`, existing.id); err != nil {
		return fmt.Errorf("remove direct activation: %w", err)
	}
	removed, err := dropUnbackedProjection(ctx, tx, existing.id)
	if err != nil {
		return err
	}
	var trash projectionTrash
	if removed {
		trash.targets = []string{filepath.Join(target.directory, skill.Name)}
	}
	return trash.commit(ctx, tx, "deactivation")
}

// coveringGroupName 读取覆盖该投影的组激活中 ID 最小的 Group 名。
func coveringGroupName(ctx context.Context, q rowQuerier, id SkillID, scope scopeRow) (string, error) {
	var name string
	err := q.QueryRowContext(ctx, `
		SELECT groups.name FROM group_activations ga
		JOIN skill_groups sg ON sg.group_id = ga.group_id
		JOIN groups ON groups.id = ga.group_id
		WHERE sg.skill_id = ? AND ga.agent = ? AND ga.project_id IS ?
		ORDER BY groups.id LIMIT 1`, id, scope.agent, scope.projectID).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("read covering group: %w", err)
	}
	return name, nil
}

// planMember 判定 skill 作为组成员在 target 上的结果:已有投影只补上组激活;同名成员(duplicate)、
// 目标被占用、有未记录文件夹(且未选 Overwrite)的跳过;其余返回待落盘的 placement。planned 记下本批已计划的
// 目标文件夹,两个 Scope 解析到同一目录时后者按占用跳过。
func (l *sqliteLibrary) planMember(ctx context.Context, operation string, id SkillID, name string, target resolvedScope, opts ActivateOptions, duplicate bool, planned map[string]bool) (GroupMemberResult, *placement, error) {
	result := GroupMemberResult{SkillID: id, Scope: target.scope()}
	existing, err := findProjection(ctx, l.db, id, target.scopeRow)
	if err != nil {
		return result, nil, err
	}
	if existing != nil {
		result.Outcome = MemberAlreadyProjected
		return result, nil, nil
	}
	result.Outcome = MemberSkipped
	if duplicate {
		result.Error = preconditionError(operation, "multiple members project to the same target", ErrorDetails{Name: name, Path: filepath.Join(target.directory, name)})
		return result, nil, nil
	}
	path := filepath.Join(target.directory, name)
	if planned[path] {
		result.Error = preconditionError(operation, "another scope already projects this skill into the same directory", ErrorDetails{Path: target.directory})
		return result, nil, nil
	}
	if result.Error, err = l.placementConflict(ctx, operation, id, name, target, opts.OnConflict); err != nil || result.Error != nil {
		return result, nil, err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return result, nil, err
	}
	planned[path] = true
	result.Outcome = MemberProjected
	return result, &placement{skill: skill, scope: target}, nil
}

func (l *sqliteLibrary) ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) (results []GroupMemberResult, err error) {
	// 整组操作持独占锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("activate group", opts.OnConflict); err != nil {
		return nil, err
	}
	target, err := l.resolveScope(ctx, "activate group", scope)
	if err != nil {
		return nil, err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return nil, err
	}
	// 组成员可跨来源并重名,但同名成员会投影到同一路径;同名的都跳过,不按顺序先到先得。
	counts := make(map[string]int, len(members))
	for _, member := range members {
		counts[member.Name]++
	}
	results = make([]GroupMemberResult, 0, len(members))
	var placements []*placement
	planned := map[string]bool{}
	for _, member := range members {
		result, item, err := l.planMember(ctx, "activate group", member.ID, member.Name, target, opts, counts[member.Name] > 1, planned)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		if item != nil {
			placements = append(placements, item)
		}
	}
	now := clockNow()
	err = l.place(ctx, placements, func(tx *sql.Tx) error {
		if err := recordGroupActivationTx(ctx, tx, groupID, target.scopeRow, now); err != nil {
			return err
		}
		for _, item := range placements {
			if err := recordProjectionTx(ctx, tx, item.skill.ID, item.scope.scopeRow, false, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
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
	var activationID int64
	err = l.db.QueryRowContext(ctx, `SELECT id FROM group_activations WHERE group_id = ? AND agent = ? AND project_id IS ?`,
		groupID, target.agent, target.projectID).Scan(&activationID)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: NotFound, Message: "deactivate group: group is not active in scope"}
	}
	if err != nil {
		return fmt.Errorf("find group activation: %w", err)
	}
	// 单连接约束下,先在开事务前把成员投影与名字取齐(事务期间不能再走 l.db)。
	members, err := l.memberProjections(ctx, groupID, 0, target.scopeRow)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group deactivation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_activations WHERE id = ?`, activationID); err != nil {
		return fmt.Errorf("remove group activation: %w", err)
	}
	var trash projectionTrash
	for _, member := range members {
		removed, err := dropUnbackedProjection(ctx, tx, member.id)
		if err != nil {
			return err
		}
		if removed {
			trash.targets = append(trash.targets, filepath.Join(target.directory, member.name))
		}
	}
	return trash.commit(ctx, tx, "group deactivation")
}

// memberProjection 是组成员在某 Scope 上的一条投影:记录 ID、skill 名与 Scope 身份。
type memberProjection struct {
	id   int64
	name string
	scopeRow
}

// memberProjections 列出组成员在该组已激活的 Scope 上的投影,按 skill 与 Scope 排序;skillID 非 0 时只看该成员,
// scope 的 agent 非空时只看该 Scope。
func (l *sqliteLibrary) memberProjections(ctx context.Context, groupID GroupID, skillID SkillID, scope scopeRow) ([]memberProjection, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT p.id, s.name, p.agent, p.project_id FROM projections p
		JOIN skills s ON s.id = p.skill_id
		JOIN skill_groups sg ON sg.skill_id = p.skill_id AND sg.group_id = ?
		JOIN group_activations ga ON ga.group_id = sg.group_id AND ga.agent = p.agent AND ga.project_id IS p.project_id
		WHERE (? = 0 OR p.skill_id = ?) AND (? = '' OR (p.agent = ? AND p.project_id IS ?))
		ORDER BY p.skill_id, p.agent, p.project_id`,
		groupID, skillID, skillID, scope.agent, scope.agent, scope.projectID)
	if err != nil {
		return nil, fmt.Errorf("list member projections: %w", err)
	}
	var members []memberProjection
	for rows.Next() {
		var member memberProjection
		if err := rows.Scan(&member.id, &member.name, &member.agent, &member.projectID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read member projection: %w", err)
		}
		members = append(members, member)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read member projections: %w", err)
	}
	return members, nil
}

func (l *sqliteLibrary) ListGroupActivations(ctx context.Context) (activations []GroupActivation, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.readGroupActivations(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		activations = append(activations, GroupActivation{GroupID: row.groupID, Scope: row.scope(), ActivatedAt: row.activatedAt})
	}
	return activations, nil
}

// groupActivationRow 是一条组激活记录的落库形状。
type groupActivationRow struct {
	groupID     GroupID
	activatedAt time.Time
	scopeRow
}

// readGroupActivations 读出组激活,按 Group、Agent、项目排序(全局在前);groupID 非 0 时只读该组。
func (l *sqliteLibrary) readGroupActivations(ctx context.Context, groupID GroupID) ([]groupActivationRow, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT group_id, agent, project_id, activated_at FROM group_activations
		WHERE ? = 0 OR group_id = ? ORDER BY group_id, agent, project_id`, groupID, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group activations: %w", err)
	}
	var activations []groupActivationRow
	for rows.Next() {
		var (
			row         groupActivationRow
			activatedAt string
		)
		if err := rows.Scan(&row.groupID, &row.agent, &row.projectID, &activatedAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read group activation: %w", err)
		}
		if row.activatedAt, err = parseStoredTime(activatedAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read group activation: %w", err)
		}
		activations = append(activations, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read group activations: %w", err)
	}
	return activations, nil
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
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id, agent, project_id, direct, activated_at FROM projections ORDER BY skill_id, agent, project_id`)
	if err != nil {
		return nil, fmt.Errorf("list projections: %w", err)
	}
	defer rows.Close()
	var scopes []scopeRow
	for rows.Next() {
		var (
			projection  Projection
			row         scopeRow
			activatedAt string
		)
		if err := rows.Scan(&projection.SkillID, &row.agent, &row.projectID, &projection.Direct, &activatedAt); err != nil {
			return nil, fmt.Errorf("read projection: %w", err)
		}
		if projection.ActivatedAt, err = parseStoredTime(activatedAt); err != nil {
			return nil, fmt.Errorf("read projection: %w", err)
		}
		projection.Scope = row.scope()
		scopes = append(scopes, row)
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read projections: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close projections: %w", err)
	}
	for i := range projections {
		// 目录是派生值:按现行布局 Resolve,库里不存路径。
		if projections[i].Directory, err = l.Resolve(ctx, projections[i].Scope); err != nil {
			return nil, err
		}
		// 覆盖它的组激活由成员关系推出:该 skill 是成员且组已激活到同一 Scope。
		rows, err := l.db.QueryContext(ctx, `
			SELECT ga.group_id FROM group_activations ga JOIN skill_groups sg ON sg.group_id = ga.group_id
			WHERE sg.skill_id = ? AND ga.agent = ? AND ga.project_id IS ? ORDER BY ga.group_id`,
			projections[i].SkillID, scopes[i].agent, scopes[i].projectID)
		if err != nil {
			return nil, fmt.Errorf("list covering groups: %w", err)
		}
		for rows.Next() {
			var id GroupID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("read covering group: %w", err)
			}
			projections[i].Groups = append(projections[i].Groups, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, fmt.Errorf("read covering groups: %w", err)
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
		return "", fmt.Errorf("inspect projection: %w", err)
	}

	if _, err := os.Lstat(projection.Directory); errors.Is(err, os.ErrNotExist) {
		return ProjectionDirectoryMissing, nil
	} else if err != nil {
		return "", fmt.Errorf("inspect projection directory: %w", err)
	}

	target := filepath.Join(projection.Directory, name)
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return ProjectionMissing, nil
	} else if err != nil {
		return "", fmt.Errorf("inspect projection target: %w", err)
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
