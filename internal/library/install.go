package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
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
	exists, err := agentdir.DirectoryExists(s.root)
	if err != nil {
		return nil, fmt.Errorf("inspect scope root: %w", err)
	}
	if exists {
		return nil, nil
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
	root, directory, err := l.scopeDirectory(ctx, row)
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
	WHERE sg.skill_id = skill_scopes.skill_id AND ga.agent = skill_scopes.agent
		AND ga.project_id IS skill_scopes.project_id)`

type skillScopeRow struct {
	id     int64
	direct bool
}

func findSkillScope(ctx context.Context, q rowQuerier, id SkillID, scope scopeRow) (*skillScopeRow, error) {
	var row skillScopeRow
	err := q.QueryRowContext(ctx, `SELECT id, direct FROM skill_scopes WHERE skill_id = ? AND agent = ? AND project_id IS ?`, id, scope.agent, scope.projectID).Scan(&row.id, &row.direct)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find skill scope: %w", err)
	}
	return &row, nil
}

func uninstallMember(ctx context.Context, tx *sql.Tx, skillScopeID int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM skill_scopes WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, skillScopeID)
	if err != nil {
		return false, fmt.Errorf("remove skill scope: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove skill scope: %w", err)
	}
	return removed > 0, nil
}

func keepAsDirectInstall(ctx context.Context, tx *sql.Tx, skillScopeID int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE skill_scopes SET direct = 1 WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, skillScopeID); err != nil {
		return fmt.Errorf("keep skill scope as direct install: %w", err)
	}
	return nil
}

type claimant struct {
	skillID SkillID
	name    string
	scopeRow
}

func (l *sqliteLibrary) targetClaimant(ctx context.Context, id SkillID, name string, target resolvedScope) (*claimant, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT p.skill_id, s.name, p.agent, p.project_id FROM skill_scopes p
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
	want := agentdir.Canonical(target.directory)
	for _, other := range claimants {
		directory, err := l.Resolve(ctx, other.scope())
		if err != nil {
			return nil, err
		}
		if agentdir.Canonical(directory) != want {
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
	exists, err := agentdir.Exists(path)
	if err != nil {
		return nil, fmt.Errorf("inspect target folder: %w", err)
	}
	if exists && policy != Overwrite {
		return existsError(operation, "target already exists", ErrorDetails{Path: path}), nil
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
	existing, err := findSkillScope(ctx, l.db, id, scope.scopeRow)
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
	exists, err := agentdir.Exists(plan.Target)
	if err != nil {
		return plan, fmt.Errorf("inspect target folder: %w", err)
	}
	if exists {
		plan.Status = InstallUntracked
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
	existing, err := findSkillScope(ctx, l.db, id, target.scopeRow)
	if err != nil {
		return err
	}
	if existing != nil {
		if _, err := l.db.ExecContext(ctx, `UPDATE skill_scopes SET direct = 1 WHERE id = ?`, existing.id); err != nil {
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
		return recordSkillScopeTx(ctx, tx, skill.ID, target.scopeRow, true, clockNow())
	})
}

type placement struct {
	skill Skill
	scope resolvedScope
}

func (l *sqliteLibrary) place(ctx context.Context, placements []*placement, record func(*sql.Tx) error) (err error) {
	for _, item := range placements {
		if err := item.scope.requireRoot("place skill folder"); err != nil {
			return err
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	for _, item := range placements {
		if err := change.Stage(ctx, item.scope.directory, item.skill.Name, item.skill.Files); err != nil {
			return err
		}
	}
	if record == nil {
		if err := change.Commit(ctx); err != nil {
			return err
		}
		change.Done()
		return nil
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin skill scope record: %w", err)
	}
	defer tx.Rollback()
	if err := record(tx); err != nil {
		return err
	}
	return commitChange(ctx, tx, &change, "skill scope record")
}

func commitChange(ctx context.Context, tx *sql.Tx, change *agentdir.Change, operation string) error {
	if err := change.Commit(ctx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", operation, err)
	}
	change.Done()
	return nil
}

func recordSkillScopeTx(ctx context.Context, tx *sql.Tx, id SkillID, scope scopeRow, direct bool, createdAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_scopes(skill_id, agent, project_id, direct, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, scope.agent, scope.projectID, direct, formatStoredTime(createdAt)); err != nil {
		return fmt.Errorf("record skill scope: %w", err)
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
	existing, err := findSkillScope(ctx, l.db, id, target.scopeRow)
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
		return preconditionError("uninstall skill", "skill is only installed here by a group install", ErrorDetails{Name: name})
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin uninstall: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE skill_scopes SET direct = 0 WHERE id = ?`, existing.id); err != nil {
		return fmt.Errorf("remove direct install: %w", err)
	}
	removed, err := uninstallMember(ctx, tx, existing.id)
	if err != nil {
		return err
	}
	if removed {
		change.Remove(target.directory, skill.Name)
	}
	return commitChange(ctx, tx, &change, "uninstall")
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
	existing, err := findSkillScope(ctx, l.db, id, target.scopeRow)
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
		plan.planned[filepath.Join(agentdir.Canonical(target.directory), name)] = true
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
	if plan.planned[filepath.Join(agentdir.Canonical(target.directory), name)] {
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
			if err := recordSkillScopeTx(ctx, tx, item.skill.ID, item.scope.scopeRow, false, now); err != nil {
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
	members, err := l.memberSkillScopes(ctx, groupID, 0, target.scopeRow)
	if err != nil {
		return err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group uninstall: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_installs WHERE id = ?`, groupInstallID); err != nil {
		return fmt.Errorf("remove group install: %w", err)
	}
	for _, member := range members {
		removed, err := uninstallMember(ctx, tx, member.id)
		if err != nil {
			return err
		}
		if removed {
			change.Remove(target.directory, member.name)
		}
	}
	return commitChange(ctx, tx, &change, "group uninstall")
}

type memberSkillScope struct {
	id   int64
	name string
	scopeRow
}

func (l *sqliteLibrary) memberSkillScopes(ctx context.Context, groupID GroupID, skillID SkillID, scope scopeRow) ([]memberSkillScope, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT p.id, s.name, p.agent, p.project_id FROM skill_scopes p
		JOIN skills s ON s.id = p.skill_id
		JOIN skill_groups sg ON sg.skill_id = p.skill_id AND sg.group_id = ?
		JOIN group_installs ga ON ga.group_id = sg.group_id AND ga.agent = p.agent AND ga.project_id IS p.project_id
		WHERE (? = 0 OR p.skill_id = ?) AND (? = '' OR (p.agent = ? AND p.project_id IS ?))
		ORDER BY p.skill_id, p.agent, p.project_id`,
		groupID, skillID, skillID, scope.agent, scope.agent, scope.projectID)
	if err != nil {
		return nil, fmt.Errorf("list member skill scopes: %w", err)
	}
	var members []memberSkillScope
	for rows.Next() {
		var member memberSkillScope
		if err := rows.Scan(&member.id, &member.name, &member.agent, &member.projectID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read member skill scope: %w", err)
		}
		members = append(members, member)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read member skill scopes: %w", err)
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

func (l *sqliteLibrary) ListSkillScopes(ctx context.Context) (skillScopes []SkillScope, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.listSkillScopes(ctx, 0, nil)
}

func (l *sqliteLibrary) listSkillScopes(ctx context.Context, skillID SkillID, scope *scopeRow) (skillScopes []SkillScope, err error) {
	var filter scopeRow
	if scope != nil {
		filter = *scope
	}
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id, agent, project_id, direct, created_at FROM skill_scopes
		WHERE (? = 0 OR skill_id = ?) AND (? = '' OR (agent = ? AND project_id IS ?))
		ORDER BY skill_id, agent, project_id`,
		skillID, skillID, filter.agent, filter.agent, filter.projectID)
	if err != nil {
		return nil, fmt.Errorf("list skill scopes: %w", err)
	}
	defer rows.Close()
	var scopes []scopeRow
	for rows.Next() {
		var (
			skillScope SkillScope
			row        scopeRow
			createdAt  string
		)
		if err := rows.Scan(&skillScope.SkillID, &row.agent, &row.projectID, &skillScope.Direct, &createdAt); err != nil {
			return nil, fmt.Errorf("read skill scope: %w", err)
		}
		if skillScope.CreatedAt, err = parseStoredTime(createdAt); err != nil {
			return nil, fmt.Errorf("read skill scope: %w", err)
		}
		skillScope.Scope = row.scope()
		scopes = append(scopes, row)
		skillScopes = append(skillScopes, skillScope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read skill scopes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close skill scopes: %w", err)
	}
	for i := range skillScopes {
		if skillScopes[i].Directory, err = l.Resolve(ctx, skillScopes[i].Scope); err != nil {
			return nil, err
		}
		rows, err := l.db.QueryContext(ctx, `
			SELECT ga.group_id FROM group_installs ga JOIN skill_groups sg ON sg.group_id = ga.group_id
			WHERE sg.skill_id = ? AND ga.agent = ? AND ga.project_id IS ? ORDER BY ga.group_id`,
			skillScopes[i].SkillID, scopes[i].agent, scopes[i].projectID)
		if err != nil {
			return nil, fmt.Errorf("list covering groups: %w", err)
		}
		for rows.Next() {
			var id GroupID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("read covering group: %w", err)
			}
			skillScopes[i].Groups = append(skillScopes[i].Groups, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, fmt.Errorf("read covering groups: %w", err)
		}
	}
	return skillScopes, nil
}

func (l *sqliteLibrary) filteredSkillScopes(ctx context.Context, operation string, filter SkillScopeFilter) ([]SkillScope, error) {
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

func (l *sqliteLibrary) InspectSkillScopes(ctx context.Context, filter SkillScopeFilter) (inspections []SkillScopeInspection, err error) {
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

func (l *sqliteLibrary) inspectSkillScope(ctx context.Context, skillScope SkillScope) (SkillScopeStatus, error) {
	var name string
	var currentVersionID int64
	if err := l.db.QueryRowContext(ctx, `SELECT name, current_version_id FROM skills WHERE id = ?`, skillScope.SkillID).Scan(&name, &currentVersionID); err != nil {
		return "", fmt.Errorf("inspect skill scope: %w", err)
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

	current, err := l.versionFingerprints(ctx, currentVersionID)
	if err != nil {
		return "", err
	}
	if maps.Equal(disk, current) {
		return SkillScopeCurrent, nil
	}

	rows, err := l.db.QueryContext(ctx, `SELECT id FROM skill_versions WHERE skill_id = ? AND id != ? ORDER BY id DESC`, skillScope.SkillID, currentVersionID)
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
			return SkillScopeStale, nil
		}
	}
	return SkillScopeModified, nil
}

func (l *sqliteLibrary) versionFingerprints(ctx context.Context, versionID int64) (map[string]agentdir.Meta, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT path, fingerprint, mode FROM version_files WHERE version_id = ?`, versionID)
	if err != nil {
		return nil, fmt.Errorf("list version fingerprints: %w", err)
	}
	defer rows.Close()
	files := make(map[string]agentdir.Meta)
	for rows.Next() {
		var path string
		var meta agentdir.Meta
		if err := rows.Scan(&path, &meta.Fingerprint, &meta.Mode); err != nil {
			return nil, fmt.Errorf("read version fingerprint: %w", err)
		}
		files[path] = meta
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read version fingerprints: %w", err)
	}
	return files, nil
}
