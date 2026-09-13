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

func (l *sqliteLibrary) removeProjectedCopy(ctx context.Context, id SkillID, scope Scope) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(scope.Path, skill.Name)); err != nil {
		return fmt.Errorf("remove projected copy: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.activate(ctx, id, scope, opts, true, 0)
}

func (l *sqliteLibrary) activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions, individual bool, groupID GroupID) error {
	if err := validateScope(scope); err != nil {
		return err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	// 已有本 skill 在该 Scope 的投影记录时,目标目录是我们自己的投影,再次激活应当
	// 幂等成功(刷新内容 / 补记依据),而非因目录存在而报冲突。
	active, err := l.hasActivation(ctx, id, scope)
	if err != nil {
		return err
	}
	target := filepath.Join(scope.Path, skill.Name)
	if _, err := os.Lstat(target); err == nil {
		if !active && opts.OnConflict != Overwrite {
			return &Error{Code: AlreadyExists, Message: "activate skill: target already exists"}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect activation target: %w", err)
	}
	if err := os.MkdirAll(scope.Path, 0o755); err != nil {
		return fmt.Errorf("create scope: %w", err)
	}
	stage, err := l.stageSkill(scope, skill)
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
	if err := recordActivationTx(ctx, tx, id, scope, individual, groupID); err != nil {
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

// stageSkill 把 skill 的全部文件写入 scope 下的一个临时暂存目录,返回暂存路径。
// 暂存好但尚未移入目标位置,便于分组激活先把所有成员备齐再统一落盘。
func (l *sqliteLibrary) stageSkill(scope Scope, skill Skill) (string, error) {
	stage, err := os.MkdirTemp(scope.Path, "."+skill.Name+"-")
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
func recordActivationTx(ctx context.Context, tx *sql.Tx, id SkillID, scope Scope, individual bool, groupID GroupID) error {
	individualValue := 0
	if individual {
		individualValue = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (?, ?, ?, ?)
		ON CONFLICT(skill_id, agent, scope_path) DO UPDATE SET individual = MAX(individual, excluded.individual)
	`, id, scope.Agent, scope.Path, individualValue); err != nil {
		return fmt.Errorf("record activation: %w", err)
	}
	if groupID != 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO activation_groups(activation_id, skill_id, agent, scope_path, group_id)
			SELECT id, skill_id, agent, scope_path, ? FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?
		`, groupID, id, scope.Agent, scope.Path); err != nil {
			return fmt.Errorf("record group activation: %w", err)
		}
	}
	return nil
}

func (l *sqliteLibrary) Deactivate(ctx context.Context, id SkillID, scope Scope) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateScope(scope); err != nil {
		return err
	}
	var individual bool
	if err := l.db.QueryRowContext(ctx, `SELECT individual FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path).Scan(&individual); err != nil {
		return fmt.Errorf("find activation: %w", err)
	}
	if !individual {
		return &Error{Code: NotFound, Message: "deactivate skill: no independent activation"}
	}
	return l.deactivateOrigin(ctx, id, scope)
}

func (l *sqliteLibrary) deactivateOrigin(ctx context.Context, id SkillID, scope Scope) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deactivation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE activations SET individual = 0 WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path); err != nil {
		return fmt.Errorf("remove independent activation: %w", err)
	}
	keep, err := keepActivation(ctx, tx, id, scope)
	if err != nil {
		return err
	}
	if !keep {
		if err := os.RemoveAll(filepath.Join(scope.Path, skill.Name)); err != nil {
			return fmt.Errorf("deactivate skill: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path); err != nil {
			return fmt.Errorf("remove activation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deactivation: %w", err)
	}
	return nil
}

// keepActivation 在撤销依据后判断投影是否仍需保留。
func keepActivation(ctx context.Context, tx *sql.Tx, id SkillID, scope Scope) (bool, error) {
	var keep bool
	if err := tx.QueryRowContext(ctx, `
		SELECT individual OR EXISTS(
			SELECT 1 FROM activation_groups
			WHERE skill_id = ? AND agent = ? AND scope_path = ?
		)
		FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?
	`, id, scope.Agent, scope.Path, id, scope.Agent, scope.Path).Scan(&keep); err != nil {
		return false, fmt.Errorf("check activation origins: %w", err)
	}
	return keep, nil
}

func (l *sqliteLibrary) ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) (err error) {
	// ponytail: 整组操作独占库锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx = context.WithValue(ctx, libraryOperationKey{}, l)
	defer func() { err = typedError(err) }()
	if err := validateScope(scope); err != nil {
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
			return &Error{Code: AlreadyExists, Message: fmt.Sprintf("activate group: multiple members named %q project to the same target", member.Name)}
		}
		seen[member.Name] = true
	}
	if opts.OnConflict != Overwrite {
		for _, member := range members {
			// 已属本组/本 skill 自己的既有投影不算冲突,分组再次激活应幂等成功。
			active, err := l.hasActivation(ctx, member.ID, scope)
			if err != nil {
				return err
			}
			if active {
				continue
			}
			if _, err := os.Lstat(filepath.Join(scope.Path, member.Name)); err == nil {
				return &Error{Code: AlreadyExists, Message: "activate group: target already exists"}
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect activation target: %w", err)
			}
		}
	}
	if len(members) == 0 {
		return nil
	}
	if err := os.MkdirAll(scope.Path, 0o755); err != nil {
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
		stage, err := l.stageSkill(scope, skill)
		if err != nil {
			return errors.Join(err, rollback())
		}
		applied = append(applied, &staged{id: skill.ID, target: filepath.Join(scope.Path, skill.Name), stage: stage})
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
		if err := recordActivationTx(ctx, tx, member.id, scope, false, groupID); err != nil {
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
	// ponytail: 整组操作独占库锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx = context.WithValue(ctx, libraryOperationKey{}, l)
	defer func() { err = typedError(err) }()
	if err := validateScope(scope); err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id FROM activation_groups WHERE group_id = ? AND agent = ? AND scope_path = ? ORDER BY skill_id`, groupID, scope.Agent, scope.Path)
	if err != nil {
		return fmt.Errorf("list group activations: %w", err)
	}
	var ids []SkillID
	for rows.Next() {
		var id SkillID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read group activation: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close group activations: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read group activations: %w", err)
	}
	if len(ids) == 0 {
		return &Error{Code: NotFound, Message: "deactivate group: group is not active in scope"}
	}

	// 单连接约束下,先在开事务前把成员名字取齐(事务期间不能再走 l.db)。
	names := make(map[SkillID]string, len(ids))
	for _, id := range ids {
		skill, err := l.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		names[id] = skill.Name
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group deactivation: %w", err)
	}
	defer tx.Rollback()
	var removeTargets []string
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE skill_id = ? AND agent = ? AND scope_path = ? AND group_id = ?`, id, scope.Agent, scope.Path, groupID); err != nil {
			return fmt.Errorf("remove group activation: %w", err)
		}
		keep, err := keepActivation(ctx, tx, id, scope)
		if err != nil {
			return err
		}
		if !keep {
			if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path); err != nil {
				return fmt.Errorf("remove activation: %w", err)
			}
			removeTargets = append(removeTargets, filepath.Join(scope.Path, names[id]))
		}
	}

	// 磁盘删除做成可逆:先把待删目录改名到暂存,提交后再真正删除;
	// 任一步失败则撤销改名并回滚事务,该组去激活全有或全无。
	type trashedProjection struct{ trash, target string }
	var trashed []trashedProjection
	restore := func() error {
		var restoreErr error
		for i := len(trashed) - 1; i >= 0; i-- {
			restoreErr = errors.Join(restoreErr, os.Rename(trashed[i].trash, trashed[i].target))
		}
		return restoreErr
	}
	for _, target := range removeTargets {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, restore())
		}
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return errors.Join(fmt.Errorf("inspect deactivation target: %w", err), restore())
		}
		trash, err := randomName(filepath.Join(scope.Path, ".deactivate-"))
		if err != nil {
			return errors.Join(err, restore())
		}
		if err := os.Rename(target, trash); err != nil {
			return errors.Join(fmt.Errorf("deactivate skill: %w", err), restore())
		}
		trashed = append(trashed, trashedProjection{trash: trash, target: target})
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(fmt.Errorf("commit group deactivation: %w", err), restore())
	}
	for _, entry := range trashed {
		_ = os.RemoveAll(entry.trash)
	}
	return nil
}

func randomName(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}

// hasActivation 判断某 skill 在某 Scope 是否已有投影记录(不论依据是独立还是分组)。
func (l *sqliteLibrary) hasActivation(ctx context.Context, id SkillID, scope Scope) (bool, error) {
	var exists int
	err := l.db.QueryRowContext(ctx, `SELECT 1 FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check activation: %w", err)
	}
	return true, nil
}

func (l *sqliteLibrary) ListActivations(ctx context.Context) (activations []Activation, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id, agent, scope_path, individual FROM activations ORDER BY skill_id, agent, scope_path`)
	if err != nil {
		return nil, fmt.Errorf("list activations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var activation Activation
		if err := rows.Scan(&activation.SkillID, &activation.Scope.Agent, &activation.Scope.Path, &activation.Individual); err != nil {
			return nil, fmt.Errorf("read activation: %w", err)
		}
		activations = append(activations, activation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read activations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close activations: %w", err)
	}
	for i := range activations {
		rows, err := l.db.QueryContext(ctx, `SELECT group_id FROM activation_groups WHERE skill_id = ? AND agent = ? AND scope_path = ? ORDER BY group_id`, activations[i].SkillID, activations[i].Scope.Agent, activations[i].Scope.Path)
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
