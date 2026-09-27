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
	"slices"
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

// scope 把落库列还原为技能库对外的 Scope 身份。
func (r scopeRow) scope() Scope {
	if r.projectID == nil {
		return GlobalScope(r.agent)
	}
	return ProjectScope(r.agent, *r.projectID)
}

// resolvedScope 是一次安装操作的内部目标:Scope 的落库身份 + 根目录 + 按现行布局算出的目录。
type resolvedScope struct {
	scopeRow
	root      string // 作用域根目录;Library 从不创建它,只创建其下的 Agent 布局目录
	directory string
}

// rootMissing 确认作用域根目录存在且是目录;不满足时返回 FailedPrecondition(Details.Path 为根目录,
// 项目 Scope 另带 ProjectID),说明这个项目或主目录不在本机。读取失败放在第二个返回值。
func (s resolvedScope) rootMissing(operation string) (*Error, error) {
	info, err := os.Stat(s.root)
	if err == nil && info.IsDir() {
		return nil, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect scope root: %w", err)
	}
	details := ErrorDetails{Path: s.root}
	if s.projectID != nil {
		details.ProjectID = *s.projectID
	}
	return preconditionError(operation, "scope root does not exist", details), nil
}

// requireRoot 是 rootMissing 的错误形式,供整次操作以根目录为前提的调用方使用。
func (s resolvedScope) requireRoot(operation string) error {
	missing, err := s.rootMissing(operation)
	if err != nil {
		return err
	}
	if missing != nil {
		return missing
	}
	return nil
}

// resolveScope 把技能库收到的 Scope 变成内部目标:先按调用方的操作名校验组合,
// 未知 Agent / 未登记项目的 NotFound 来自 scopeLocation。
func (l *sqliteLibrary) resolveScope(ctx context.Context, operation string, scope Scope) (resolvedScope, error) {
	row, err := scopeOf(operation, scope)
	if err != nil {
		return resolvedScope{}, err
	}
	root, directory, err := l.scopeLocation(ctx, row)
	if err != nil {
		return resolvedScope{}, err
	}
	return resolvedScope{scopeRow: row, root: root, directory: directory}, nil
}

// rowQuerier 让 *sql.DB 与 *sql.Tx 共用同一个单行查询。
type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// groupCoverageSQL 判断 installations 当前行是否被组安装覆盖:该 skill 是某组成员且该组已安装到同一 Scope。
// 组安装撑着安装项的事实只由它推出,不另存。
const groupCoverageSQL = `EXISTS (
	SELECT 1 FROM group_installs ga JOIN skill_groups sg ON sg.group_id = ga.group_id
	WHERE sg.skill_id = installations.skill_id AND ga.agent = installations.agent
		AND ga.project_id IS installations.project_id)`

// installationRow 是一个已存在的安装项记录:稳定 ID 与直接安装标记。
type installationRow struct {
	id     int64
	direct bool
}

// findInstallation 找到某 skill 在某 Scope 的安装项记录;不存在时返回 nil 而非错误。
func findInstallation(ctx context.Context, q rowQuerier, id SkillID, scope scopeRow) (*installationRow, error) {
	var row installationRow
	err := q.QueryRowContext(ctx, `SELECT id, direct FROM installations WHERE skill_id = ? AND agent = ? AND project_id IS ?`, id, scope.agent, scope.projectID).Scan(&row.id, &row.direct)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find installation: %w", err)
	}
	return &row, nil
}

// dropUnbackedInstallation 在撤销某个安装后,删掉不再有任何安装撑着的安装项记录;返回是否删除。
// 与 keepUnbackedInstallation 相对:撤回时用它,保留时用后者。
func dropUnbackedInstallation(ctx context.Context, tx *sql.Tx, installationID int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM installations WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, installationID)
	if err != nil {
		return false, fmt.Errorf("remove installation: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove installation: %w", err)
	}
	return removed > 0, nil
}

// keepUnbackedInstallation 在撤掉某个组安装后,让不再有任何安装撑着的安装项改为直接安装,磁盘不动
// (移出成员的 keep 与删除分组的非破坏规则)。
func keepUnbackedInstallation(ctx context.Context, tx *sql.Tx, installationID int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE installations SET direct = 1 WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, installationID); err != nil {
		return fmt.Errorf("keep installation as direct install: %w", err)
	}
	return nil
}

// installationTrash 让"删记录 + 删安装项文件夹"可逆:调用方在事务内删完记录、把待删路径放进 targets,
// 再由 commit 收尾:改名到同一父目录下的暂存名 → 提交事务 → 真正删除;提交前任一步失败改回原名,
// 配合调用方的 Rollback 使库和磁盘都回到操作前。
// RemoveSkill / UnregisterProject / Uninstall / UninstallGroup / RemoveSkillFromGroup 共用,
// 避免任何一处再出现"先删盘后写库"。
type installationTrash struct {
	targets []string
	trashed []struct{ trash, target string } // 已改名的条目:原路径与暂存路径
}

// commit 完成收尾;operation 用于提交失败的错误消息。暂存目录的最终删除失败只留下垃圾,不影响权威数据。
func (t *installationTrash) commit(ctx context.Context, tx *sql.Tx, operation string) error {
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
func (t *installationTrash) trash(ctx context.Context) error {
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
			return errors.Join(fmt.Errorf("remove installation folder: %w", err), t.restore())
		}
		t.trashed = append(t.trashed, struct{ trash, target string }{staged, path})
	}
	return nil
}

// restore 把全部已改名的暂存目录改回原名,供提交前失败时复位。
func (t *installationTrash) restore() error {
	var restoreErr error
	for _, entry := range t.trashed {
		restoreErr = errors.Join(restoreErr, os.Rename(entry.trash, entry.target))
	}
	return restoreErr
}

// claimant 是一个可能与待安装目标争同一文件夹的既有安装项:所属 skill、技能名与 Scope 身份。
type claimant struct {
	skillID SkillID
	name    string
	scopeRow
}

// targetClaimant 在写事务外查找目标占用者,供安装计划与执行共用。调用方持共享或独占锁,
// 并已确认本技能在本作用域没有记录;所有目录均按现行布局解析。
func (l *sqliteLibrary) targetClaimant(ctx context.Context, id SkillID, name string, target resolvedScope) (*claimant, error) {
	// 同名即包含本 skill 自身;本 skill 的其他 Scope 排在前面,使既有的"共享目录"错误形状不因同名 skill 的存在而改变。
	rows, err := l.db.QueryContext(ctx, `
		SELECT p.skill_id, s.name, p.agent, p.project_id FROM installations p
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
	// 按规范化路径比较:主目录或 Agent 布局目录可能经符号链接指到另一个 Scope 的目录。
	want := canonicalDirectory(target.directory)
	for _, other := range claimants {
		directory, err := l.Resolve(ctx, other.scope())
		if err != nil {
			return nil, err
		}
		if canonicalDirectory(directory) != want {
			continue
		}
		return &other, nil
	}
	return nil, nil
}

// placementConflict 检查尚无安装项的 skill 能否在 target 新建安装项:目标被另一个安装项占用返回 FailedPrecondition,
// 有未记录的文件夹且未选 Overwrite 返回 AlreadyExists;能安装返回 nil。查询或磁盘读取失败放在第二个返回值。
func (l *sqliteLibrary) placementConflict(ctx context.Context, operation string, id SkillID, name string, target resolvedScope, policy ConflictPolicy) (*Error, error) {
	other, err := l.targetClaimant(ctx, id, name, target)
	if err != nil {
		return nil, err
	}
	if other != nil && other.skillID == id {
		return preconditionError(operation, "another scope already installs this skill into the same directory", ErrorDetails{Path: target.directory}), nil
	}
	if other != nil {
		return preconditionError(operation, "another skill already installs this name into the same directory", ErrorDetails{
			Path: filepath.Join(target.directory, other.name), Name: other.name, SkillID: other.skillID,
		}), nil
	}
	path := filepath.Join(target.directory, name)
	if _, err := os.Lstat(path); err == nil {
		if policy != Overwrite {
			return existsError(operation, "target already exists", ErrorDetails{Path: path}), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect installation target: %w", err)
	}
	return nil, nil
}

func (l *sqliteLibrary) PlanInstall(ctx context.Context, id SkillID, scope Scope) (plan InstallPlan, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "plan install", scope)
	if err != nil {
		return plan, err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return plan, err
	}
	if err := target.requireRoot("plan install"); err != nil {
		return plan, err
	}
	return l.planInstall(ctx, skill.ID, skill.Name, target)
}

func (l *sqliteLibrary) PlanGroupInstall(ctx context.Context, groupID GroupID, scope Scope) (plan GroupInstallPlan, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "plan group install", scope)
	if err != nil {
		return plan, err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return plan, err
	}
	if err := target.requireRoot("plan group install"); err != nil {
		return plan, err
	}
	counts := memberNameCounts(members)
	for _, member := range members {
		if counts[member.Name] > 1 && !slices.Contains(plan.DuplicateNames, member.Name) {
			plan.DuplicateNames = append(plan.DuplicateNames, member.Name)
		}
	}
	plan.Members = make([]InstallPlan, 0, len(members))
	for _, member := range members {
		item, err := l.planInstall(ctx, member.ID, member.Name, target)
		if err != nil {
			return GroupInstallPlan{}, err
		}
		// 与执行时同序:已有安装项的照常补组安装,其余同名成员先于占用 / 未记录文件夹判定为跳过。
		if counts[member.Name] > 1 && item.Status != InstallAlreadyInstalled {
			item.Status = InstallDuplicate
			item.OccupierSkillID, item.OccupierName = 0, ""
		}
		plan.Members = append(plan.Members, item)
	}
	return plan, nil
}

// memberNameCounts 统计组成员里每个名字出现的次数;大于 1 的是组内同名,安装项会落到同一路径。
func memberNameCounts(members []SkillSummary) map[string]int {
	counts := make(map[string]int, len(members))
	for _, member := range members {
		counts[member.Name]++
	}
	return counts
}

func (l *sqliteLibrary) planInstall(ctx context.Context, id SkillID, name string, scope resolvedScope) (InstallPlan, error) {
	plan := InstallPlan{SkillID: id, Name: name, Directory: scope.directory, Target: filepath.Join(scope.directory, name), Status: InstallReady}
	existing, err := findInstallation(ctx, l.db, id, scope.scopeRow)
	if err != nil {
		return plan, err
	}
	if existing != nil {
		plan.Status = InstallAlreadyInstalled
		return plan, nil
	}
	other, err := l.targetClaimant(ctx, id, name, scope)
	if err != nil {
		return plan, err
	}
	if other != nil {
		plan.Status = InstallOccupied
		plan.OccupierSkillID = other.skillID
		plan.OccupierName = other.name
		return plan, nil
	}
	if _, err := os.Lstat(plan.Target); err == nil {
		plan.Status = InstallUntracked
	} else if !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("inspect installation target: %w", err)
	}
	return plan, nil
}

func (l *sqliteLibrary) Install(ctx context.Context, id SkillID, scope Scope, opts InstallOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("install", opts.OnConflict); err != nil {
		return err
	}
	target, err := l.resolveScope(ctx, "install", scope)
	if err != nil {
		return err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	if err := target.requireRoot("install skill"); err != nil {
		return err
	}
	existing, err := findInstallation(ctx, l.db, id, target.scopeRow)
	if err != nil {
		return err
	}
	if existing != nil {
		// 已有安装项:只补上直接安装,不写盘;把文件夹带到当前版本是单独的重新安装。
		if _, err := l.db.ExecContext(ctx, `UPDATE installations SET direct = 1 WHERE id = ?`, existing.id); err != nil {
			return fmt.Errorf("record direct install: %w", err)
		}
		return nil
	}
	conflict, err := l.placementConflict(ctx, "install skill", skill.ID, skill.Name, target, opts.OnConflict)
	if err != nil {
		return err
	}
	if conflict != nil {
		return conflict
	}
	placements := []*placement{{skill: skill, scope: target}}
	return l.place(ctx, placements, func(tx *sql.Tx) error {
		return recordInstallationTx(ctx, tx, skill.ID, target.scopeRow, true, clockNow())
	})
}

// placement 是一次落盘里的一个安装项:把 skill 的当前版本写进 scope 的目录(新建,或重新安装时替换已有安装项)。
type placement struct {
	skill  Skill
	scope  resolvedScope
	folder string // 目标文件夹
	stage  string // 暂存目录;移入目标后置空
	backup string // 被替换的原有目标(外来文件夹或重新安装前的安装项)的备份;"" 表示原本无目标
	moved  bool   // 暂存是否已移入目标位置
}

// place 把一批安装项作为一个操作单元落盘:先全部暂存,再逐个移入目标,最后在单事务里调用 record;
// record 为 nil 时(重新安装)只落盘、不开事务。任何一步失败都复原本次的磁盘改动(含被替换的原有目标)且不留记录。
// placements 为空时只执行 record。调用方持独占锁并已排除冲突。
func (l *sqliteLibrary) place(ctx context.Context, placements []*placement, record func(*sql.Tx) error) error {
	rollback := func() error {
		var restoreErr error
		for i := len(placements) - 1; i >= 0; i-- {
			item := placements[i]
			if item.moved {
				if err := os.RemoveAll(item.folder); err != nil {
					restoreErr = errors.Join(restoreErr, err)
				} else if item.backup != "" {
					// 复位失败时保留备份,避免丢失用户原始内容。
					restoreErr = errors.Join(restoreErr, os.Rename(item.backup, item.folder))
				}
			}
			if item.stage != "" {
				restoreErr = errors.Join(restoreErr, os.RemoveAll(item.stage))
			}
		}
		return restoreErr
	}
	// 共用写盘路径:根目录不存在时一律不落盘(MkdirAll 会凭空建出整条路径)。
	for _, item := range placements {
		if err := item.scope.requireRoot("place installation"); err != nil {
			return err
		}
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
		item.folder = filepath.Join(item.scope.directory, item.skill.Name)
	}
	for _, item := range placements {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollback())
		}
		backup, err := moveStageIntoPlace(item.stage, item.folder)
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
	if record != nil {
		tx, err := l.db.BeginTx(ctx, nil)
		if err != nil {
			return errors.Join(fmt.Errorf("begin installation record: %w", err), rollback())
		}
		defer tx.Rollback()
		if err := record(tx); err != nil {
			return errors.Join(err, rollback())
		}
		if err := tx.Commit(); err != nil {
			return errors.Join(fmt.Errorf("commit installation record: %w", err), rollback())
		}
	}
	for _, item := range placements {
		if item.backup != "" {
			_ = os.RemoveAll(item.backup)
		}
	}
	return nil
}

// stageSkill 把 skill 的全部文件写入 directory 下的一个临时暂存目录,返回暂存路径。
// 暂存好但尚未移入目标位置,便于一批安装项先全部备齐再统一落盘。
func stageSkill(directory string, skill Skill) (string, error) {
	stage, err := os.MkdirTemp(directory, "."+skill.Name+"-")
	if err != nil {
		return "", fmt.Errorf("stage installation: %w", err)
	}
	for _, file := range skill.Files {
		if !filepath.IsLocal(file.Path) {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("invalid skill path: %s", file.Path)
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("create installation directory: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("write installation file: %w", err)
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
			return "", fmt.Errorf("replace installation: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect installation target: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return "", fmt.Errorf("place installation: %w", err)
	}
	return backup, nil
}

// recordInstallationTx 在事务内为刚落盘的安装项建记录;direct 表示是否由直接安装撑着。只带 Scope 身份,不存目录。
func recordInstallationTx(ctx context.Context, tx *sql.Tx, id SkillID, scope scopeRow, direct bool, createdAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO installations(skill_id, agent, project_id, direct, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, scope.agent, scope.projectID, direct, formatStoredTime(createdAt)); err != nil {
		return fmt.Errorf("record installation: %w", err)
	}
	return nil
}

// recordGroupInstallTx 在事务内记下组在 Scope 的组安装;已有则保持原记录(含时间)。
func recordGroupInstallTx(ctx context.Context, tx *sql.Tx, groupID GroupID, scope scopeRow, createdAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO group_installs(group_id, agent, project_id, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		groupID, scope.agent, scope.projectID, formatStoredTime(createdAt)); err != nil {
		return fmt.Errorf("record group install: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) Uninstall(ctx context.Context, id SkillID, scope Scope) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "uninstall", scope)
	if err != nil {
		return err
	}
	existing, err := findInstallation(ctx, l.db, id, target.scopeRow)
	if err != nil {
		return err
	}
	if existing == nil {
		return &Error{Code: NotFound, Message: "uninstall skill: skill is not installed in scope"}
	}
	if !existing.direct {
		name, err := coveringGroupName(ctx, l.db, id, target.scopeRow)
		if err != nil {
			return err
		}
		return preconditionError("uninstall skill", "installation is only backed by a group", ErrorDetails{Name: name})
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin uninstall: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE installations SET direct = 0 WHERE id = ?`, existing.id); err != nil {
		return fmt.Errorf("remove direct install: %w", err)
	}
	removed, err := dropUnbackedInstallation(ctx, tx, existing.id)
	if err != nil {
		return err
	}
	var trash installationTrash
	if removed {
		trash.targets = []string{filepath.Join(target.directory, skill.Name)}
	}
	return trash.commit(ctx, tx, "uninstall")
}

// coveringGroupName 读取覆盖该安装项的组安装中 ID 最小的 Group 名。
func coveringGroupName(ctx context.Context, q rowQuerier, id SkillID, scope scopeRow) (string, error) {
	var name string
	err := q.QueryRowContext(ctx, `
		SELECT groups.name FROM group_installs ga
		JOIN skill_groups sg ON sg.group_id = ga.group_id
		JOIN groups ON groups.id = ga.group_id
		WHERE sg.skill_id = ? AND ga.agent = ? AND ga.project_id IS ?
		ORDER BY groups.id LIMIT 1`, id, scope.agent, scope.projectID).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("read covering group: %w", err)
	}
	return name, nil
}

// memberPlan 累积一批组成员的判定:逐条结果与待落盘的新安装项。operation 与 opts 在整批内不变。
type memberPlan struct {
	operation  string
	opts       InstallOptions
	results    []GroupMemberResult
	placements []*placement
	planned    map[string]bool // 本批已计划的目标文件夹(规范化路径),两个 Scope 实际落在同一目录时后者按占用跳过
}

func newMemberPlan(operation string, opts InstallOptions) *memberPlan {
	return &memberPlan{operation: operation, opts: opts, results: []GroupMemberResult{}, planned: map[string]bool{}}
}

// planMember 判定 skill 作为组成员在 target 上的结果并记入 plan:已有安装项只补上组安装;有 memberBlocker
// 的跳过;其余加入待落盘的新安装项。
func (l *sqliteLibrary) planMember(ctx context.Context, plan *memberPlan, id SkillID, name string, target resolvedScope, duplicate bool) error {
	result := GroupMemberResult{SkillID: id, Scope: target.scope(), Outcome: MemberSkipped}
	existing, err := findInstallation(ctx, l.db, id, target.scopeRow)
	if err != nil {
		return err
	}
	if existing != nil {
		result.Outcome = MemberAlreadyInstalled
	} else if result.Error, err = l.memberBlocker(ctx, plan, id, name, target, duplicate); err != nil {
		return err
	} else if result.Error == nil {
		skill, err := l.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		plan.planned[filepath.Join(canonicalDirectory(target.directory), name)] = true
		plan.placements = append(plan.placements, &placement{skill: skill, scope: target})
		result.Outcome = MemberInstalled
	}
	plan.results = append(plan.results, result)
	return nil
}

// memberBlocker 返回尚无安装项的成员不能在 target 新建安装项的原因,依次检查:作用域根目录不存在、组内同名
// (duplicate)、本批另一个 Scope 已计划同一目标、目标被占用或有未记录文件夹(且未选 Overwrite);能落盘返回 nil。
func (l *sqliteLibrary) memberBlocker(ctx context.Context, plan *memberPlan, id SkillID, name string, target resolvedScope, duplicate bool) (*Error, error) {
	if missing, err := target.rootMissing(plan.operation); err != nil || missing != nil {
		return missing, err
	}
	path := filepath.Join(target.directory, name)
	if duplicate {
		return preconditionError(plan.operation, "multiple members install to the same target", ErrorDetails{Name: name, Path: path}), nil
	}
	if plan.planned[filepath.Join(canonicalDirectory(target.directory), name)] {
		return preconditionError(plan.operation, "another scope already installs this skill into the same directory", ErrorDetails{Path: target.directory}), nil
	}
	return l.placementConflict(ctx, plan.operation, id, name, target, plan.opts.OnConflict)
}

// applyMemberPlan 落盘 plan 里的新安装项,并在同一事务里先执行 record(组安装或成员关系),再为新安装项建记录
// (都由组安装撑着,无直接安装)。成功返回逐条结果;失败时整批复原。
func (l *sqliteLibrary) applyMemberPlan(ctx context.Context, plan *memberPlan, record func(tx *sql.Tx, now time.Time) error) ([]GroupMemberResult, error) {
	now := clockNow()
	err := l.place(ctx, plan.placements, func(tx *sql.Tx) error {
		if err := record(tx, now); err != nil {
			return err
		}
		for _, item := range plan.placements {
			if err := recordInstallationTx(ctx, tx, item.skill.ID, item.scope.scopeRow, false, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return plan.results, nil
}

func (l *sqliteLibrary) InstallGroup(ctx context.Context, groupID GroupID, scope Scope, opts InstallOptions) (_ []GroupMemberResult, err error) {
	// 整组操作持独占锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("install group", opts.OnConflict); err != nil {
		return nil, err
	}
	target, err := l.resolveScope(ctx, "install group", scope)
	if err != nil {
		return nil, err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	// 根目录不存在是作用域的问题,不是某个成员的冲突:整次失败,不记组安装。
	if err := target.requireRoot("install group"); err != nil {
		return nil, err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return nil, err
	}
	// 组成员可跨来源并重名,但同名成员会安装到同一路径;同名的都跳过,不按顺序先到先得。
	counts := memberNameCounts(members)
	plan := newMemberPlan("install group", opts)
	for _, member := range members {
		if err := l.planMember(ctx, plan, member.ID, member.Name, target, counts[member.Name] > 1); err != nil {
			return nil, err
		}
	}
	return l.applyMemberPlan(ctx, plan, func(tx *sql.Tx, now time.Time) error {
		return recordGroupInstallTx(ctx, tx, groupID, target.scopeRow, now)
	})
}

func (l *sqliteLibrary) UninstallGroup(ctx context.Context, groupID GroupID, scope Scope) (err error) {
	// 整组操作持独占锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "uninstall group", scope)
	if err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	var groupInstallID int64
	err = l.db.QueryRowContext(ctx, `SELECT id FROM group_installs WHERE group_id = ? AND agent = ? AND project_id IS ?`,
		groupID, target.agent, target.projectID).Scan(&groupInstallID)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: NotFound, Message: "uninstall group: group is not installed in scope"}
	}
	if err != nil {
		return fmt.Errorf("find group install: %w", err)
	}
	// 单连接约束下,先在开事务前把成员安装项与名字取齐(事务期间不能再走 l.db)。
	members, err := l.memberInstallations(ctx, groupID, 0, target.scopeRow)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group uninstall: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_installs WHERE id = ?`, groupInstallID); err != nil {
		return fmt.Errorf("remove group install: %w", err)
	}
	var trash installationTrash
	for _, member := range members {
		removed, err := dropUnbackedInstallation(ctx, tx, member.id)
		if err != nil {
			return err
		}
		if removed {
			trash.targets = append(trash.targets, filepath.Join(target.directory, member.name))
		}
	}
	return trash.commit(ctx, tx, "group uninstall")
}

// memberInstallation 是组成员在某 Scope 上的一个安装项:记录 ID、skill 名与 Scope 身份。
type memberInstallation struct {
	id   int64
	name string
	scopeRow
}

// memberInstallations 列出组成员在该组已安装的 Scope 上的安装项,按 skill 与 Scope 排序;skillID 非 0 时只看该成员,
// scope 的 agent 非空时只看该 Scope。
func (l *sqliteLibrary) memberInstallations(ctx context.Context, groupID GroupID, skillID SkillID, scope scopeRow) ([]memberInstallation, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT p.id, s.name, p.agent, p.project_id FROM installations p
		JOIN skills s ON s.id = p.skill_id
		JOIN skill_groups sg ON sg.skill_id = p.skill_id AND sg.group_id = ?
		JOIN group_installs ga ON ga.group_id = sg.group_id AND ga.agent = p.agent AND ga.project_id IS p.project_id
		WHERE (? = 0 OR p.skill_id = ?) AND (? = '' OR (p.agent = ? AND p.project_id IS ?))
		ORDER BY p.skill_id, p.agent, p.project_id`,
		groupID, skillID, skillID, scope.agent, scope.agent, scope.projectID)
	if err != nil {
		return nil, fmt.Errorf("list member installations: %w", err)
	}
	var members []memberInstallation
	for rows.Next() {
		var member memberInstallation
		if err := rows.Scan(&member.id, &member.name, &member.agent, &member.projectID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read member installation: %w", err)
		}
		members = append(members, member)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read member installations: %w", err)
	}
	return members, nil
}

func (l *sqliteLibrary) ListGroupInstalls(ctx context.Context) (installs []GroupInstall, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.readGroupInstalls(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		installs = append(installs, GroupInstall{GroupID: row.groupID, Scope: row.scope(), CreatedAt: row.createdAt})
	}
	return installs, nil
}

// groupInstallRow 是一条组安装记录的落库形状。
type groupInstallRow struct {
	groupID   GroupID
	createdAt time.Time
	scopeRow
}

// readGroupInstalls 读出组安装,按 Group、Agent、项目排序(全局在前);groupID 非 0 时只读该组。
func (l *sqliteLibrary) readGroupInstalls(ctx context.Context, groupID GroupID) ([]groupInstallRow, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT group_id, agent, project_id, created_at FROM group_installs
		WHERE ? = 0 OR group_id = ? ORDER BY group_id, agent, project_id`, groupID, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group installs: %w", err)
	}
	var installs []groupInstallRow
	for rows.Next() {
		var (
			row       groupInstallRow
			createdAt string
		)
		if err := rows.Scan(&row.groupID, &row.agent, &row.projectID, &createdAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read group install: %w", err)
		}
		if row.createdAt, err = parseStoredTime(createdAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read group install: %w", err)
		}
		installs = append(installs, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read group install: %w", err)
	}
	return installs, nil
}

func randomName(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}

func (l *sqliteLibrary) ListInstallations(ctx context.Context) (installations []Installation, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.listInstallations(ctx, 0, nil)
}

// listInstallations 按 ListInstallations 的顺序读出安装项;skillID 非 0 时只看该技能,scope 非 nil 时只看该 Scope。
// 调用方持共享或独占锁。
func (l *sqliteLibrary) listInstallations(ctx context.Context, skillID SkillID, scope *scopeRow) (installations []Installation, err error) {
	var filter scopeRow
	if scope != nil {
		filter = *scope
	}
	// 全局(project_id 为 NULL)排在同一 Agent 的各项目 Scope 之前。
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id, agent, project_id, direct, created_at FROM installations
		WHERE (? = 0 OR skill_id = ?) AND (? = '' OR (agent = ? AND project_id IS ?))
		ORDER BY skill_id, agent, project_id`,
		skillID, skillID, filter.agent, filter.agent, filter.projectID)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}
	defer rows.Close()
	var scopes []scopeRow
	for rows.Next() {
		var (
			installation Installation
			row          scopeRow
			createdAt    string
		)
		if err := rows.Scan(&installation.SkillID, &row.agent, &row.projectID, &installation.Direct, &createdAt); err != nil {
			return nil, fmt.Errorf("read installation: %w", err)
		}
		if installation.CreatedAt, err = parseStoredTime(createdAt); err != nil {
			return nil, fmt.Errorf("read installation: %w", err)
		}
		installation.Scope = row.scope()
		scopes = append(scopes, row)
		installations = append(installations, installation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read installations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close installations: %w", err)
	}
	for i := range installations {
		// 目录是派生值:按现行布局 Resolve,库里不存路径。
		if installations[i].Directory, err = l.Resolve(ctx, installations[i].Scope); err != nil {
			return nil, err
		}
		// 覆盖它的组安装由成员关系推出:该 skill 是成员且组已安装到同一 Scope。
		rows, err := l.db.QueryContext(ctx, `
			SELECT ga.group_id FROM group_installs ga JOIN skill_groups sg ON sg.group_id = ga.group_id
			WHERE sg.skill_id = ? AND ga.agent = ? AND ga.project_id IS ? ORDER BY ga.group_id`,
			installations[i].SkillID, scopes[i].agent, scopes[i].projectID)
		if err != nil {
			return nil, fmt.Errorf("list covering groups: %w", err)
		}
		for rows.Next() {
			var id GroupID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("read covering group: %w", err)
			}
			installations[i].Groups = append(installations[i].Groups, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, fmt.Errorf("read covering groups: %w", err)
		}
	}
	return installations, nil
}

// filteredInstallations 校验筛选条件后按 ListInstallations 的顺序读出范围内的安装项:Scope 须合法且可解析,
// 技能须存在;已知但没有安装项时返回空。持共享锁,嵌套在调用方的锁内时复用。
func (l *sqliteLibrary) filteredInstallations(ctx context.Context, operation string, filter InstallationFilter) ([]Installation, error) {
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
	return l.listInstallations(ctx, filter.SkillID, scope)
}

func (l *sqliteLibrary) InspectInstallations(ctx context.Context, filter InstallationFilter) (inspections []InstallationInspection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	installations, err := l.filteredInstallations(ctx, "inspect installations", filter)
	if err != nil {
		return nil, err
	}
	// 与 Reinstall 一致:没有安装项时返回空切片而非 nil。
	inspections = []InstallationInspection{}
	for _, installation := range installations {
		status, err := l.inspectInstallation(ctx, installation)
		if err != nil {
			return nil, err
		}
		inspections = append(inspections, InstallationInspection{Installation: installation, Status: status})
	}
	return inspections, nil
}

// inspectInstallation 按 InstallationStatus 注释中的顺序判定一个安装项。
func (l *sqliteLibrary) inspectInstallation(ctx context.Context, installation Installation) (InstallationStatus, error) {
	var name string
	var currentVersionID int64
	if err := l.db.QueryRowContext(ctx, `SELECT name, current_version_id FROM skills WHERE id = ?`, installation.SkillID).Scan(&name, &currentVersionID); err != nil {
		return "", fmt.Errorf("inspect installation: %w", err)
	}

	if _, err := os.Lstat(installation.Directory); errors.Is(err, os.ErrNotExist) {
		return InstallationDirectoryMissing, nil
	} else if err != nil {
		return "", fmt.Errorf("inspect installation directory: %w", err)
	}

	target := filepath.Join(installation.Directory, name)
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return InstallationMissing, nil
	} else if err != nil {
		return "", fmt.Errorf("inspect installation target: %w", err)
	}

	disk, err := hashFiles(ctx, target)
	if err != nil {
		if errors.Is(err, errIrregularFile) {
			return InstallationModified, nil
		}
		return "", err
	}

	current, err := l.versionFingerprints(ctx, currentVersionID)
	if err != nil {
		return "", err
	}
	if maps.Equal(disk, current) {
		return InstallationCurrent, nil
	}

	rows, err := l.db.QueryContext(ctx, `SELECT id FROM skill_versions WHERE skill_id = ? AND id != ? ORDER BY id DESC`, installation.SkillID, currentVersionID)
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
			return InstallationStale, nil
		}
	}
	return InstallationModified, nil
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
