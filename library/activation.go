package library

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// scopeRow 是 Scope 身份的落库形状:Agent 代码 + 可选项目(nil 为全局)。
type scopeRow struct {
	agent     AgentCode
	projectID *ProjectID
}

// scopeOf 把 Scope 身份拆成落库列;nil 或未知实现返回 false。
func scopeOf(scope Scope) (scopeRow, bool) {
	switch scope := scope.(type) {
	case GlobalScope:
		return scopeRow{agent: scope.Agent}, true
	case ProjectScope:
		return scopeRow{agent: scope.Agent, projectID: new(scope.ProjectID)}, true
	default:
		return scopeRow{}, false
	}
}

// scope 把落库列还原为门面上的 Scope 身份。
func (r scopeRow) scope() Scope {
	if r.projectID == nil {
		return GlobalScope{Agent: r.agent}
	}
	return ProjectScope{Agent: r.agent, ProjectID: *r.projectID}
}

// resolvedScope 是一次激活操作的内部目标:Scope 的落库身份 + 按现行布局算出的目录。
type resolvedScope struct {
	scopeRow
	directory string
}

// resolveScope 把门面收到的 Scope 变成内部目标;未知 Agent / 未登记项目的 NotFound 来自 Resolve。
func (l *sqliteLibrary) resolveScope(ctx context.Context, scope Scope) (resolvedScope, error) {
	directory, err := l.Resolve(ctx, scope)
	if err != nil {
		return resolvedScope{}, err
	}
	row, _ := scopeOf(scope) // Resolve 已拒绝 nil 与未知实现
	return resolvedScope{scopeRow: row, directory: directory}, nil
}

// rowQuerier 让 *sql.DB 与 *sql.Tx 共用同一个单行查询。
type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// activationRow 是一条已存在的投影记录:稳定 ID 与独立标记。
type activationRow struct {
	id         int64
	individual bool
}

// findActivation 找到某 skill 在某 Scope 的投影记录;不存在时返回 nil 而非错误。
func findActivation(ctx context.Context, q rowQuerier, id SkillID, scope scopeRow) (*activationRow, error) {
	var row activationRow
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

// directoryClaimedByAnotherScope 判断该 skill 是否已有另一条不同身份的 Activation 解析到同一目录
// (例如把 $HOME 本身登记为项目)。这种情况下不新建第二条记录去争同一个文件夹。
// 它在写事务之外用 l.db 查询:调用方(Activate / ActivateGroup)已持有库锁,期间没有并发写入,
// 且单连接模式下事务内不能再走 l.db 调 Resolve。
func (l *sqliteLibrary) directoryClaimedByAnotherScope(ctx context.Context, id SkillID, target resolvedScope) (bool, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT agent, project_id FROM activations WHERE skill_id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("list skill activations: %w", err)
	}
	var others []scopeRow
	for rows.Next() {
		var row scopeRow
		if err := rows.Scan(&row.agent, &row.projectID); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("read skill activation: %w", err)
		}
		others = append(others, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, fmt.Errorf("read skill activations: %w", err)
	}
	for _, other := range others {
		directory, err := l.Resolve(ctx, other.scope())
		if err != nil {
			return false, err
		}
		if directory == target.directory {
			return true, nil
		}
	}
	return false, nil
}

func (l *sqliteLibrary) Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) (err error) {
	ctx, unlock := l.writeOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("activate", opts.OnConflict); err != nil {
		return err
	}
	target, err := l.resolveScope(ctx, scope)
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
	// 幂等成功(刷新内容 / 补记依据),而非因目录存在而报冲突。
	existing, err := findActivation(ctx, l.db, id, scope.scopeRow)
	if err != nil {
		return err
	}
	if existing == nil {
		if claimed, err := l.directoryClaimedByAnotherScope(ctx, id, scope); err != nil {
			return err
		} else if claimed {
			return preconditionError("activate skill", "another scope already projects this skill into the same directory", ErrorDetails{Path: scope.directory})
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
	if err := recordActivationTx(ctx, tx, id, scope.scopeRow, individual, groupID); err != nil {
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
// 暂存好但尚未移入目标位置,便于分组激活先把所有成员备齐再统一落盘。
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

// recordActivationTx 在事务内写入投影记录:活动行 + 可选的分组依据行。
// 已有记录只补独立标记;新记录只带 Scope 身份,不存目录。
func recordActivationTx(ctx context.Context, tx *sql.Tx, id SkillID, scope scopeRow, individual bool, groupID GroupID) error {
	var activationID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO activations(skill_id, agent, project_id, individual) VALUES (?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET individual = MAX(individual, excluded.individual)
		RETURNING id`, id, scope.agent, scope.projectID, individual).Scan(&activationID)
	if err != nil {
		return fmt.Errorf("record activation: %w", err)
	}
	if groupID != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO activation_groups(activation_id, group_id) VALUES (?, ?)`, activationID, groupID); err != nil {
			return fmt.Errorf("record group activation: %w", err)
		}
	}
	return nil
}

func (l *sqliteLibrary) Deactivate(ctx context.Context, id SkillID, scope Scope) (err error) {
	ctx, unlock := l.writeOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, scope)
	if err != nil {
		return err
	}
	existing, err := findActivation(ctx, l.db, id, target.scopeRow)
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
	return l.deactivateOrigin(ctx, id, existing.id, target.directory)
}

// deactivateOrigin 撤销独立依据;没有其他依据时连同磁盘副本一起删除该记录。
func (l *sqliteLibrary) deactivateOrigin(ctx context.Context, id SkillID, activationID int64, directory string) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deactivation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE activations SET individual = 0 WHERE id = ?`, activationID); err != nil {
		return fmt.Errorf("remove independent activation: %w", err)
	}
	keep, err := keepActivation(ctx, tx, activationID)
	if err != nil {
		return err
	}
	var trash projectionTrash
	if !keep {
		if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE id = ?`, activationID); err != nil {
			return fmt.Errorf("remove activation: %w", err)
		}
		trash.targets = []string{filepath.Join(directory, skill.Name)}
	}
	return trash.commit(ctx, tx, "deactivation")
}

// keepActivation 在撤销依据后判断投影是否仍需保留。
func keepActivation(ctx context.Context, tx *sql.Tx, activationID int64) (bool, error) {
	var keep bool
	if err := tx.QueryRowContext(ctx, `
		SELECT individual OR EXISTS(SELECT 1 FROM activation_groups WHERE activation_id = ?)
		FROM activations WHERE id = ?
	`, activationID, activationID).Scan(&keep); err != nil {
		return false, fmt.Errorf("check activation origins: %w", err)
	}
	return keep, nil
}

func (l *sqliteLibrary) ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) (err error) {
	// 整组操作独占库锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	ctx, unlock := l.writeOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("activate group", opts.OnConflict); err != nil {
		return err
	}
	target, err := l.resolveScope(ctx, scope)
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
		// 已属本组/本 skill 自己的既有投影不算冲突,分组再次激活应幂等成功。
		existing, err := findActivation(ctx, l.db, member.ID, target.scopeRow)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		if claimed, err := l.directoryClaimedByAnotherScope(ctx, member.ID, target); err != nil {
			return err
		} else if claimed {
			return preconditionError("activate group", "another scope already projects a member into the same directory", ErrorDetails{Path: target.directory})
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
	// 任何一步失败都回滚本次操作的磁盘改动与记录,既有的独立激活不受影响。
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
		if err := recordActivationTx(ctx, tx, member.id, target.scopeRow, false, groupID); err != nil {
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
	// 整组操作独占库锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	ctx, unlock := l.writeOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, scope)
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
	type groupActivation struct {
		activationID int64
		skillID      SkillID
	}
	var members []groupActivation
	for rows.Next() {
		var member groupActivation
		if err := rows.Scan(&member.activationID, &member.skillID); err != nil {
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE activation_id = ? AND group_id = ?`, member.activationID, groupID); err != nil {
			return fmt.Errorf("remove group activation: %w", err)
		}
		keep, err := keepActivation(ctx, tx, member.activationID)
		if err != nil {
			return err
		}
		if !keep {
			if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE id = ?`, member.activationID); err != nil {
				return fmt.Errorf("remove activation: %w", err)
			}
			trash.targets = append(trash.targets, filepath.Join(target.directory, names[member.skillID]))
		}
	}

	return trash.commit(ctx, tx, "group deactivation")
}

// supportingGroupName 读取仅由分组支撑的投影所经由的第一个分组名。
func supportingGroupName(ctx context.Context, q rowQuerier, activationID int64) (string, error) {
	var name string
	err := q.QueryRowContext(ctx, `
		SELECT groups.name FROM activation_groups
		JOIN groups ON groups.id = activation_groups.group_id
		WHERE activation_groups.activation_id = ?
		ORDER BY groups.id LIMIT 1`, activationID).Scan(&name)
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

func (l *sqliteLibrary) ListActivations(ctx context.Context) (activations []Activation, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	// 全局(project_id 为 NULL)排在同一 Agent 的各项目 Scope 之前。
	rows, err := l.db.QueryContext(ctx, `SELECT id, skill_id, agent, project_id, individual FROM activations ORDER BY skill_id, agent, project_id`)
	if err != nil {
		return nil, fmt.Errorf("list activations: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var (
			activation Activation
			id         int64
			row        scopeRow
		)
		if err := rows.Scan(&id, &activation.SkillID, &row.agent, &row.projectID, &activation.Individual); err != nil {
			return nil, fmt.Errorf("read activation: %w", err)
		}
		activation.Scope = row.scope()
		ids = append(ids, id)
		activations = append(activations, activation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read activations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close activations: %w", err)
	}
	for i := range activations {
		// 目录是派生值:按现行布局 Resolve,库里不存路径。
		if activations[i].Directory, err = l.Resolve(ctx, activations[i].Scope); err != nil {
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
			activations[i].ViaGroups = append(activations[i].ViaGroups, id)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close activation groups: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read activation groups: %w", err)
		}
	}
	return activations, nil
}
