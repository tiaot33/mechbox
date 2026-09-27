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

type scopeRow struct {
	agent     AgentCode
	projectID *ProjectID
}

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

func (r scopeRow) scope() Scope {
	if r.projectID == nil {
		return GlobalScope(r.agent)
	}
	return ProjectScope(r.agent, *r.projectID)
}

type resolvedScope struct {
	scopeRow
	root      string
	directory string
}

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

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const groupCoverageSQL = `EXISTS (
	SELECT 1 FROM group_installs ga JOIN skill_groups sg ON sg.group_id = ga.group_id
	WHERE sg.skill_id = installations.skill_id AND ga.agent = installations.agent
		AND ga.project_id IS installations.project_id)`

type installationRow struct {
	id     int64
	direct bool
}

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

func keepUnbackedInstallation(ctx context.Context, tx *sql.Tx, installationID int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE installations SET direct = 1 WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, installationID); err != nil {
		return fmt.Errorf("keep installation as direct install: %w", err)
	}
	return nil
}

type installationTrash struct {
	targets []string
	trashed []struct{ trash, target string }
}

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

func (t *installationTrash) restore() error {
	var restoreErr error
	for _, entry := range t.trashed {
		restoreErr = errors.Join(restoreErr, os.Rename(entry.trash, entry.target))
	}
	return restoreErr
}

type claimant struct {
	skillID SkillID
	name    string
	scopeRow
}

func (l *sqliteLibrary) targetClaimant(ctx context.Context, id SkillID, name string, target resolvedScope) (*claimant, error) {
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
		if counts[member.Name] > 1 && item.Status != InstallAlreadyInstalled {
			item.Status = InstallDuplicate
			item.OccupierSkillID, item.OccupierName = 0, ""
		}
		plan.Members = append(plan.Members, item)
	}
	return plan, nil
}

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

type placement struct {
	skill  Skill
	scope  resolvedScope
	folder string
	stage  string
	backup string
	moved  bool
}

func (l *sqliteLibrary) place(ctx context.Context, placements []*placement, record func(*sql.Tx) error) error {
	rollback := func() error {
		var restoreErr error
		for i := len(placements) - 1; i >= 0; i-- {
			item := placements[i]
			if item.moved {
				if err := os.RemoveAll(item.folder); err != nil {
					restoreErr = errors.Join(restoreErr, err)
				} else if item.backup != "" {
					restoreErr = errors.Join(restoreErr, os.Rename(item.backup, item.folder))
				}
			}
			if item.stage != "" {
				restoreErr = errors.Join(restoreErr, os.RemoveAll(item.stage))
			}
		}
		return restoreErr
	}
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

func recordInstallationTx(ctx context.Context, tx *sql.Tx, id SkillID, scope scopeRow, direct bool, createdAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO installations(skill_id, agent, project_id, direct, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, scope.agent, scope.projectID, direct, formatStoredTime(createdAt)); err != nil {
		return fmt.Errorf("record installation: %w", err)
	}
	return nil
}

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

type memberPlan struct {
	operation  string
	opts       InstallOptions
	results    []GroupMemberResult
	placements []*placement
	planned    map[string]bool
}

func newMemberPlan(operation string, opts InstallOptions) *memberPlan {
	return &memberPlan{operation: operation, opts: opts, results: []GroupMemberResult{}, planned: map[string]bool{}}
}

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
	if err := target.requireRoot("install group"); err != nil {
		return nil, err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return nil, err
	}
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

type memberInstallation struct {
	id   int64
	name string
	scopeRow
}

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

type groupInstallRow struct {
	groupID   GroupID
	createdAt time.Time
	scopeRow
}

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

func (l *sqliteLibrary) listInstallations(ctx context.Context, skillID SkillID, scope *scopeRow) (installations []Installation, err error) {
	var filter scopeRow
	if scope != nil {
		filter = *scope
	}
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
		if installations[i].Directory, err = l.Resolve(ctx, installations[i].Scope); err != nil {
			return nil, err
		}
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
